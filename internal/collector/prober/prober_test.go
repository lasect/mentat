package prober

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"mentat/internal/clientdb"
)

type fakeDatabase struct {
	ping  func(context.Context) error
	exec  func(context.Context, string) error
	calls []string
}

func (db *fakeDatabase) Ping(ctx context.Context) error {
	if db.ping != nil {
		return db.ping(ctx)
	}
	return nil
}
func (db *fakeDatabase) Exec(ctx context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	db.calls = append(db.calls, sql)
	if db.exec != nil {
		return pgconn.CommandTag{}, db.exec(ctx, sql)
	}
	return pgconn.CommandTag{}, nil
}
func testProber(db *fakeDatabase) *prober {
	return &prober{registry: NewProberRegistry(), getDatabase: func(uuid.UUID) (probeDatabaseClient, error) { return db, nil }}
}
func assertEnvelope(t *testing.T, got ProberResult, req Request) {
	t.Helper()
	if got.EventID != req.EventID || got.DatabaseID != req.DatabaseID || got.Reason != req.Reason {
		t.Fatalf("lost request metadata: %+v", got)
	}
	if got.StartedAt.IsZero() || got.CompletedAt.Before(got.StartedAt) {
		t.Fatalf("invalid timestamps: %+v", got)
	}
}
func TestProbeSelection(t *testing.T) {
	for _, tc := range []struct {
		name            string
		names           []ExtensionName
		checks, results int
	}{
		{"all", nil, 5, 5},
		{"subset and duplicates", []ExtensionName{ExtPGStatMonitor, ExtPGStatMonitor, ExtPGQualStats}, 2, 2},
		{"unknown and supported", []ExtensionName{"other", ExtPGStatMonitor}, 1, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := &fakeDatabase{}
			req := Request{EventID: uuid.New(), DatabaseID: uuid.New(), Reason: ReasonManual, ExtensionNames: tc.names}
			got, err := testProber(db).probeDatabase(context.Background(), req)
			if err != nil || got.Status != ProbeCompleted || got.DatabaseStatus != DatabaseReachable {
				t.Fatalf("probe = %+v, %v", got, err)
			}
			assertEnvelope(t, got, req)
			if len(db.calls) != tc.checks || len(got.Extensions) != tc.results {
				t.Fatalf("checks=%d results=%d", len(db.calls), len(got.Extensions))
			}
			expected := tc.names
			if len(expected) == 0 {
				expected = []ExtensionName{ExtPGStatMonitor, ExtPGWaitSampling, ExtPGStatKCache, ExtPGQualStats, ExtPGSentinel}
			}
			for _, name := range expected {
				if _, exists := got.Extensions[name]; !exists {
					t.Fatalf("missing result for requested extension %q", name)
				}
			}
			for name, result := range got.Extensions {
				if result.Name != name {
					t.Fatalf("incorrect name: %+v", result)
				}
				if name == "other" {
					if result.Readiness != ReadinessUnsupported || result.Checked {
						t.Fatalf("unknown extension: %+v", result)
					}
				} else if result.Readiness != ReadinessReady || !result.Checked {
					t.Fatalf("supported extension: %+v", result)
				}
			}
		})
	}
}
func TestProbeUnresolvedResultPreventsCompletion(t *testing.T) {
	for _, readiness := range []Readiness{ReadinessUnknown, "", "invalid"} {
		t.Run(string(readiness), func(t *testing.T) {
			p := testProber(&fakeDatabase{})
			p.registry.registry[ExtPGStatMonitor] = ExtensionSpec{
				check: func(context.Context, Querier) ExtensionResult {
					return ExtensionResult{Readiness: readiness}
				},
			}
			req := Request{EventID: uuid.New(), DatabaseID: uuid.New(), Reason: ReasonManual,
				ExtensionNames: []ExtensionName{ExtPGStatMonitor, ExtPGQualStats}}
			got, err := p.probeDatabase(context.Background(), req)
			if err == nil || got.Status != ProbeIncomplete || got.ErrorMessage != err.Error() {
				t.Fatalf("probe = %+v, %v; want a persisted completeness failure", got, err)
			}
			assertEnvelope(t, got, req)
			if len(got.Extensions) != 2 || got.Extensions[ExtPGStatMonitor].Readiness != readiness ||
				!got.Extensions[ExtPGStatMonitor].Checked || got.Extensions[ExtPGQualStats].Readiness != ReadinessReady {
				t.Fatalf("probe did not preserve extension outcomes: %+v", got.Extensions)
			}
		})
	}
}

func TestProbeMissingPool(t *testing.T) {
	req := Request{DatabaseID: uuid.New(), EventID: uuid.New(), Reason: ReasonStartup}
	got, err := initializeProbe(NewProberRegistry(), clientdb.NewPoolManager()).probeDatabase(context.Background(), req)
	if err == nil || got.ErrorMessage != err.Error() || got.Status != ProbeIncomplete || got.DatabaseStatus != DatabaseUnknown {
		t.Fatalf("probe = %+v, %v", got, err)
	}
	assertEnvelope(t, got, req)
	for _, result := range got.Extensions {
		if result.Checked || result.Readiness != ReadinessUnavailable {
			t.Fatalf("result = %+v", result)
		}
	}
}
func TestProbePingFailure(t *testing.T) {
	failure := errors.New("connection refused")
	db := &fakeDatabase{ping: func(context.Context) error { return failure }}
	got, err := testProber(db).probeDatabase(context.Background(), Request{})
	if !errors.Is(err, failure) || got.DatabaseStatus != DatabaseUnavailable || got.Status != ProbeIncomplete || len(db.calls) != 0 {
		t.Fatalf("probe = %+v, %v", got, err)
	}
}
func TestProbeReadinessFailuresAreFindings(t *testing.T) {
	for _, tc := range []struct {
		code string
		want Readiness
	}{
		{"42P01", ReadinessMissingDependency}, {"42883", ReadinessMissingDependency},
		{"42501", ReadinessPermissionDenied}, {"0A000", ReadinessUnsupported}, {"XX000", ReadinessUnavailable},
	} {
		t.Run(tc.code, func(t *testing.T) {
			db := &fakeDatabase{}
			db.exec = func(context.Context, string) error {
				if len(db.calls) == 1 {
					return &pgconn.PgError{Code: tc.code, Message: "check failed"}
				}
				return nil
			}
			got, err := testProber(db).probeDatabase(context.Background(), Request{ExtensionNames: []ExtensionName{ExtPGStatMonitor, ExtPGQualStats}})
			if err != nil || got.Status != ProbeCompleted || got.ErrorMessage != "" || got.DatabaseStatus != DatabaseReachable {
				t.Fatalf("probe = %+v, %v", got, err)
			}
			if got.Extensions[ExtPGStatMonitor].Readiness != tc.want || got.Extensions[ExtPGStatMonitor].ErrorMessage == "" || got.Extensions[ExtPGQualStats].Readiness != ReadinessReady {
				t.Fatalf("results = %+v", got.Extensions)
			}
		})
	}
}
func TestProbeCancellationPreservesPartialResults(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db := &fakeDatabase{exec: func(context.Context, string) error { cancel(); return nil }}
	got, err := testProber(db).probeDatabase(ctx, Request{ExtensionNames: []ExtensionName{ExtPGStatMonitor, ExtPGQualStats}})
	if !errors.Is(err, context.Canceled) || got.Status != ProbeIncomplete || got.ErrorMessage != err.Error() || got.DatabaseStatus != DatabaseReachable {
		t.Fatalf("probe = %+v, %v", got, err)
	}
	first, next := got.Extensions[ExtPGStatMonitor], got.Extensions[ExtPGQualStats]
	if !first.Checked || first.Readiness != ReadinessReady || next.Checked || next.Readiness != ReadinessUnavailable || next.ErrorMessage == "" || len(db.calls) != 1 {
		t.Fatalf("results = %+v", got.Extensions)
	}
}
func TestProbeCanceledBeforeStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := testProber(&fakeDatabase{})
	p.getDatabase = func(uuid.UUID) (probeDatabaseClient, error) { t.Fatal("unexpected pool lookup"); return nil, nil }
	got, err := p.probeDatabase(ctx, Request{})
	if !errors.Is(err, context.Canceled) || got.DatabaseStatus != DatabaseUnknown || got.Status != ProbeIncomplete {
		t.Fatalf("probe = %+v, %v", got, err)
	}
}
func TestProbeConnectionLost(t *testing.T) {
	failure := errors.New("connection lost")
	db := &fakeDatabase{}
	db.exec = func(context.Context, string) error { return failure }
	db.ping = func(context.Context) error {
		if len(db.calls) > 0 {
			return failure
		}
		return nil
	}
	got, err := testProber(db).probeDatabase(context.Background(), Request{ExtensionNames: []ExtensionName{ExtPGStatMonitor, ExtPGQualStats}})
	if !errors.Is(err, failure) || got.DatabaseStatus != DatabaseUnavailable || got.Status != ProbeIncomplete || len(db.calls) != 1 {
		t.Fatalf("probe = %+v, %v", got, err)
	}
	if !got.Extensions[ExtPGStatMonitor].Checked || got.Extensions[ExtPGQualStats].Checked {
		t.Fatalf("results = %+v", got.Extensions)
	}
}
