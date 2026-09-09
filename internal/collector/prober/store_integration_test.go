package prober

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Each test uses its own schema and applies the actual probe migration. Existing
// application tables and data are never truncated by these tests.
func newProbeTestStore(t *testing.T) (*ProbeStore, *pgxpool.Pool) {
	t.Helper()
	databaseURL := os.Getenv("MENTAT_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("MENTAT_TEST_DATABASE_URL is not set")
	}
	ctx := t.Context()
	admin, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	schema := pgx.Identifier{"probe_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		_ = admin.Close(context.Background())
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.Exec(cleanupCtx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("drop probe test schema: %v", err)
		}
		_ = admin.Close(cleanupCtx)
	})
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	config.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	migration, err := os.ReadFile("../../../scripts/migrations/app/00003_probes.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, _, _ := strings.Cut(string(migration), "-- +goose Down")
	if _, err := pool.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
	store, err := NewProbeStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	return store, pool
}

func probeTestLog() ProbeLog {
	return ProbeLog{
		ID: uuid.New(), DatabaseIDs: []uuid.UUID{uuid.New(), uuid.New()},
		Reason: ReasonStartup, StartedAt: time.Now().UTC().Truncate(time.Microsecond),
	}
}

func probeTestSnapshot(log ProbeLog, index int) ProberDatabaseSnapshot {
	return ProberDatabaseSnapshot{
		EventID: log.ID, DatabaseID: log.DatabaseIDs[index], Reason: log.Reason,
		DatabaseStatus: DatabaseReachable, Status: ProbeCompleted,
		StartedAt: log.StartedAt.Add(time.Second), CompletedAt: log.StartedAt.Add(2 * time.Second),
		Extensions: map[ExtensionName]ExtensionResult{
			ExtPGStatMonitor: {Name: ExtPGStatMonitor, Readiness: ReadinessReady, Checked: true},
			ExtPGStatKCache:  {Name: ExtPGStatKCache, Readiness: ReadinessPermissionDenied, Checked: true, ErrorMessage: "permission denied"},
		},
	}
}

func TestProbeStoreLifecycle(t *testing.T) {
	store, pool := newProbeTestStore(t)
	ctx := t.Context()
	log := probeTestLog()
	if err := store.CreateLog(ctx, log); err != nil {
		t.Fatal(err)
	}
	gotLog, err := store.GetLog(ctx, log.ID)
	if err != nil || gotLog.CompletedAt != nil || gotLog.Reason != log.Reason || !reflect.DeepEqual(gotLog.DatabaseIDs, log.DatabaseIDs) || !gotLog.StartedAt.Equal(log.StartedAt) {
		t.Fatalf("initial log = %#v, %v", gotLog, err)
	}
	rows, err := store.ListDatabaseSnapshots(ctx, log.ID)
	if err != nil || len(rows) != 0 {
		t.Fatalf("initial snapshots = %#v, %v", rows, err)
	}
	first := probeTestSnapshot(log, 0)
	if err := store.SaveDatabaseSnapshot(ctx, first); err != nil {
		t.Fatal(err)
	}
	finish := log.StartedAt.Add(4 * time.Second)
	if err := store.FinishLog(ctx, log.ID, finish); !errors.Is(err, ErrProbeLogUnfinished) {
		t.Fatalf("finish with missing database = %v", err)
	}
	second := probeTestSnapshot(log, 1)
	second.StartedAt = log.StartedAt.Add(2 * time.Second)
	second.CompletedAt = log.StartedAt.Add(3 * time.Second)
	second.Status = ProbeIncomplete
	second.DatabaseStatus = DatabaseUnavailable
	second.ErrorMessage = "connection lost during probe"
	second.Extensions[ExtPGStatKCache] = ExtensionResult{
		Name: ExtPGStatKCache, Readiness: ReadinessUnavailable,
		Checked: false, ErrorMessage: second.ErrorMessage,
	}
	if err := store.SaveDatabaseSnapshot(ctx, second); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishLog(ctx, log.ID, first.CompletedAt); !errors.Is(err, ErrProbeLogUnfinished) {
		t.Fatalf("finish before last outcome = %v", err)
	}
	if err := store.FinishLog(ctx, log.ID, finish); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishLog(ctx, log.ID, finish.Add(time.Second)); err != nil {
		t.Fatalf("retry finish: %v", err)
	}
	if err := store.SaveDatabaseSnapshot(ctx, first); err != nil {
		t.Fatalf("retry snapshot after finish: %v", err)
	}
	gotLog, err = store.GetLog(ctx, log.ID)
	if err != nil || gotLog.CompletedAt == nil || !gotLog.CompletedAt.Equal(finish) {
		t.Fatalf("finished log = %#v, %v", gotLog, err)
	}
	rows, err = store.ListDatabaseSnapshots(ctx, log.ID)
	if err != nil || len(rows) != 2 {
		t.Fatalf("snapshots = %#v, %v", rows, err)
	}
	for i, want := range []ProberDatabaseSnapshot{first, second} {
		got := rows[i]
		if !got.StartedAt.Equal(want.StartedAt) || !got.CompletedAt.Equal(want.CompletedAt) {
			t.Fatalf("snapshot times = %#v, want %#v", got, want)
		}
		got.StartedAt, got.CompletedAt = want.StartedAt, want.CompletedAt
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("snapshot = %#v, want %#v", got, want)
		}
	}
	var readiness string
	if err := pool.QueryRow(ctx, `SELECT extensions->'pg_stat_kcache'->>'readiness'
		FROM probe_database_snapshots WHERE event_id = $1 AND database_id = $2`, first.EventID, first.DatabaseID).Scan(&readiness); err != nil || readiness != "permission_denied" {
		t.Fatalf("stored readiness = %q, %v", readiness, err)
	}
	changed := first
	changed.ErrorMessage = "different outcome"
	if err := store.SaveDatabaseSnapshot(ctx, changed); !errors.Is(err, ErrProbeSnapshotConflict) {
		t.Fatalf("overwrite historical snapshot = %v", err)
	}
	// A new event for the same database retains the old event's result.
	newLog := log
	newLog.ID = uuid.New()
	if err := store.CreateLog(ctx, newLog); err != nil {
		t.Fatal(err)
	}
	changed.EventID = newLog.ID
	if err := store.SaveDatabaseSnapshot(ctx, changed); err != nil {
		t.Fatal(err)
	}
	rows, err = store.ListDatabaseSnapshots(ctx, log.ID)
	if err != nil || len(rows) != 2 || rows[0].ErrorMessage != first.ErrorMessage {
		t.Fatalf("old history changed: %#v, %v", rows, err)
	}
}

func TestProbeStoreRejectsInvalidWrites(t *testing.T) {
	store, _ := newProbeTestStore(t)
	ctx := t.Context()
	log := probeTestLog()
	for _, ids := range [][]uuid.UUID{nil, {uuid.Nil}, {log.DatabaseIDs[0], log.DatabaseIDs[0]}} {
		invalid := log
		invalid.DatabaseIDs = ids
		if err := store.CreateLog(ctx, invalid); !errors.Is(err, ErrInvalidProbeData) {
			t.Fatalf("invalid targets %v: %v", ids, err)
		}
	}
	if err := store.CreateLog(ctx, log); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*ProberDatabaseSnapshot)
		want   error
	}{
		{"missing log", func(s *ProberDatabaseSnapshot) { s.EventID = uuid.New() }, ErrProbeLogNotFound},
		{"unrequested database", func(s *ProberDatabaseSnapshot) { s.DatabaseID = uuid.New() }, ErrInvalidProbeData},
		{"different reason", func(s *ProberDatabaseSnapshot) { s.Reason = ReasonManual }, ErrInvalidProbeData},
		{"before log start", func(s *ProberDatabaseSnapshot) { s.StartedAt = log.StartedAt.Add(-time.Second) }, ErrInvalidProbeData},
		{"inverted times", func(s *ProberDatabaseSnapshot) { s.CompletedAt = log.StartedAt }, ErrInvalidProbeData},
		{"invalid status", func(s *ProberDatabaseSnapshot) { s.Status = "pending" }, ErrInvalidProbeData},
		{"unresolved completed result", func(s *ProberDatabaseSnapshot) {
			s.Extensions[ExtPGStatMonitor] = ExtensionResult{Name: ExtPGStatMonitor, Readiness: ReadinessUnknown}
		}, ErrInvalidProbeData},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := probeTestSnapshot(log, 0)
			tc.change(&snapshot)
			if err := store.SaveDatabaseSnapshot(ctx, snapshot); !errors.Is(err, tc.want) {
				t.Fatalf("save error = %v, want %v", err, tc.want)
			}
		})
	}
	rows, err := store.ListDatabaseSnapshots(ctx, log.ID)
	if err != nil || len(rows) != 0 {
		t.Fatalf("rejected writes left snapshots: %#v, %v", rows, err)
	}
	if _, err := store.GetLog(ctx, uuid.New()); !errors.Is(err, ErrProbeLogNotFound) {
		t.Fatalf("missing log = %v", err)
	}
	// A probe that failed before any extension was selected still has a valid outcome.
	snapshot := probeTestSnapshot(log, 0)
	snapshot.Status, snapshot.DatabaseStatus = ProbeIncomplete, DatabaseUnknown
	snapshot.ErrorMessage, snapshot.Extensions = "pool unavailable", nil
	if err := store.SaveDatabaseSnapshot(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	rows, err = store.ListDatabaseSnapshots(ctx, log.ID)
	if err != nil || len(rows) != 1 || rows[0].Extensions == nil || len(rows[0].Extensions) != 0 {
		t.Fatalf("empty extension results = %#v, %v", rows, err)
	}
}

func TestProbeStoreConcurrentRetries(t *testing.T) {
	store, _ := newProbeTestStore(t)
	ctx := t.Context()
	log := probeTestLog()
	log.DatabaseIDs = log.DatabaseIDs[:1]
	if err := store.CreateLog(ctx, log); err != nil {
		t.Fatal(err)
	}
	snapshot := probeTestSnapshot(log, 0)
	errorsCh := make(chan error, 9)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { errorsCh <- store.SaveDatabaseSnapshot(ctx, snapshot) })
	}
	wg.Go(func() {
		err := store.FinishLog(ctx, log.ID, snapshot.CompletedAt)
		if errors.Is(err, ErrProbeLogUnfinished) {
			err = nil // Finalization may run before the first save commits.
		}
		errorsCh <- err
	})
	wg.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := store.FinishLog(ctx, log.ID, snapshot.CompletedAt); err != nil {
		t.Fatal(err)
	}
	rows, err := store.ListDatabaseSnapshots(ctx, log.ID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("concurrent retries = %#v, %v", rows, err)
	}
}
