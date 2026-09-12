package scraper

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"mentat/internal/collector/collection"
	"mentat/internal/collector/queue"
	"mentat/internal/collector/workers"
)

var _ workers.JobProcessor = (*ScraperProcess)(nil)
var failure = errors.New("fixture failure")

type fakeRows struct {
	pgx.Rows
	values            [][]any
	next              int
	closed            bool
	decodeErr, rowErr error
}

func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription {
	return []pgconn.FieldDescription{{Name: "value", DataTypeOID: 25}}
}
func (r *fakeRows) Next() bool             { r.next++; return r.next <= len(r.values) }
func (r *fakeRows) Values() ([]any, error) { return r.values[r.next-1], r.decodeErr }
func (r *fakeRows) Err() error             { return r.rowErr }
func (r *fakeRows) Close()                 { r.closed = true }

type fakeBatch struct {
	pgx.BatchResults
	rows               []*fakeRows
	index, closed      int
	queryErr, closeErr error
}

func (b *fakeBatch) Query() (pgx.Rows, error) {
	if b.queryErr != nil {
		return nil, b.queryErr
	}
	r := b.rows[b.index]
	b.index++
	return r, nil
}
func (b *fakeBatch) Close() error { b.closed++; return b.closeErr }

type fakeSender struct {
	result *fakeBatch
	sent   *pgx.Batch
}

func (s *fakeSender) SendBatch(_ context.Context, b *pgx.Batch) pgx.BatchResults {
	s.sent = b
	return s.result
}

type sinkFunc func(context.Context, CollectionResult) error

func (f sinkFunc) Store(ctx context.Context, r CollectionResult) error { return f(ctx, r) }

func testJob(t *testing.T, sql ...string) queue.CollectionJob {
	t.Helper()
	defs := make([]collection.QuerySpec, len(sql))
	for i, q := range sql {
		defs[i] = collection.QuerySpec{Extension: "fixture", ResultKey: strings.Repeat("q", i+1), SQL: q}
	}
	b, err := collection.NewBuilder(defs)
	if err != nil {
		t.Fatal(err)
	}
	p, err := b.Build([]string{"fixture"})
	if err != nil {
		t.Fatal(err)
	}
	return queue.CollectionJob{Plan: p, JobID: uuid.New(), DatabaseID: uuid.New(), ScheduledAt: time.Now()}
}

func TestScrapeResultsAndFreshBatches(t *testing.T) {
	job := testJob(t, "SELECT 1", "SELECT 2")
	job.Extensions = []string{"ignored"}
	s := &ScraperProcess{}
	var previous *pgx.Batch
	for range 2 {
		first := &fakeRows{values: [][]any{{"hello"}, {nil}}}
		empty := &fakeRows{}
		br := &fakeBatch{rows: []*fakeRows{first, empty}}
		sender := &fakeSender{result: br}
		s.getDatabase = func(id uuid.UUID) (batchSender, error) {
			if id != job.DatabaseID {
				t.Fatal("wrong database")
			}
			return sender, nil
		}
		result, err := s.Scrape(t.Context(), job)
		if err != nil {
			t.Fatal(err)
		}
		if sender.sent == previous || sender.sent.Len() != 2 || sender.sent.QueuedQueries[0].SQL != "SELECT 1" {
			t.Fatal("batch not freshly populated")
		}
		previous = sender.sent
		if result.JobID != job.JobID || result.DatabaseID != job.DatabaseID || !result.ScheduledAt.Equal(job.ScheduledAt) || result.CompletedAt.Before(result.StartedAt) {
			t.Fatal("incorrect metadata")
		}
		if len(result.Results) != 2 || result.Results[0].Extension != "fixture" || result.Results[1].ResultKey != "qq" || len(result.Results[1].Columns) != 1 || len(result.Results[1].Rows) != 0 || result.Results[0].Rows[1][0] != nil {
			t.Fatalf("incorrect results: %+v", result)
		}
		if br.closed != 1 || !first.closed || !empty.closed {
			t.Fatal("resources not closed")
		}
	}
}

func TestFailureDiscardsResultsAndSkipsSink(t *testing.T) {
	for _, kind := range []string{"query", "decode", "rows", "close", "pool", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			rows := &fakeRows{values: [][]any{{"x"}}}
			br := &fakeBatch{rows: []*fakeRows{rows}}
			sender := &fakeSender{result: br}
			called := false
			s := &ScraperProcess{getDatabase: func(uuid.UUID) (batchSender, error) { return sender, nil }, sink: sinkFunc(func(context.Context, CollectionResult) error { called = true; return nil })}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			want := failure
			switch kind {
			case "query":
				br.queryErr = failure
			case "decode":
				rows.decodeErr = failure
			case "rows":
				rows.rowErr = failure
			case "close":
				br.closeErr = failure
			case "pool":
				s.getDatabase = func(uuid.UUID) (batchSender, error) { return nil, failure }
			case "cancel":
				cancel()
				want = context.Canceled
			}
			job := testJob(t, "SELECT 1")
			result, err := s.Scrape(ctx, job)
			if !errors.Is(err, want) || !reflect.DeepEqual(result, CollectionResult{}) {
				t.Fatalf("got %+v, %v", result, err)
			}
			if kind != "pool" && kind != "cancel" && br.closed != 1 {
				t.Fatal("batch not closed")
			}
			br.index = 0
			rows.next = 0
			if err := s.Process(ctx, job); !errors.Is(err, want) || called {
				t.Fatalf("Process = %v, sink called = %v", err, called)
			}
		})
	}
}

func TestValidationAndSink(t *testing.T) {
	if _, err := NewScraper(nil, nil); err == nil {
		t.Fatal("accepted missing dependencies")
	}
	s := &ScraperProcess{getDatabase: func(uuid.UUID) (batchSender, error) { t.Fatal("lookup before validation"); return nil, nil }}
	for _, p := range []*collection.Plan{nil, {}} {
		if _, err := s.Scrape(t.Context(), queue.CollectionJob{Plan: p}); err == nil {
			t.Fatal("accepted empty plan")
		}
	}
	calls := 0
	s.getDatabase = func(uuid.UUID) (batchSender, error) {
		return &fakeSender{result: &fakeBatch{rows: []*fakeRows{{}}}}, nil
	}
	s.sink = sinkFunc(func(_ context.Context, r CollectionResult) error {
		calls++
		if len(r.Results) != 1 {
			t.Fatal("incomplete result")
		}
		return failure
	})
	if err := s.Process(t.Context(), testJob(t, "SELECT 1")); !errors.Is(err, failure) || calls != 1 {
		t.Fatalf("sink delivery: %v, %d", err, calls)
	}
}

func TestConcurrentPlanReuse(t *testing.T) {
	job := testJob(t, "SELECT 1")
	s := &ScraperProcess{getDatabase: func(uuid.UUID) (batchSender, error) {
		return &fakeSender{result: &fakeBatch{rows: []*fakeRows{{values: [][]any{{int32(1)}}}}}}, nil
	}}
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if _, err := s.Scrape(t.Context(), job); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
}
