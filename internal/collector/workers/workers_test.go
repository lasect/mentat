package workers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"mentat/internal/collector/queue"

	"github.com/google/uuid"
)

type processorFunc func(context.Context, queue.CollectionJob) error

func (f processorFunc) Process(ctx context.Context, job queue.CollectionJob) error {
	return f(ctx, job)
}

func TestWorkerPoolProcessesEveryJobAndStopsWhenQueueCloses(t *testing.T) {
	t.Parallel()

	const jobCount = 20
	jobs := make(chan queue.CollectionJob, jobCount)
	want := make(map[uuid.UUID]struct{}, jobCount)
	for range jobCount {
		jobID := uuid.New()
		want[jobID] = struct{}{}
		jobs <- queue.CollectionJob{JobID: jobID}
	}
	close(jobs)

	var mu sync.Mutex
	processed := make(map[uuid.UUID]int, jobCount)
	processor := processorFunc(func(_ context.Context, job queue.CollectionJob) error {
		mu.Lock()
		processed[job.JobID]++
		mu.Unlock()
		return nil
	})

	pool := mustNewWorkerPool(t, 3, jobs, processor, time.Second)
	waitForWorkerPool(t, runWorkerPool(pool, context.Background()))

	mu.Lock()
	defer mu.Unlock()
	for jobID := range want {
		if processed[jobID] != 1 {
			t.Errorf("job %s processed %d times, want once", jobID, processed[jobID])
		}
	}
}

func TestWorkerPoolLimitsConcurrentProcessing(t *testing.T) {
	t.Parallel()

	const workerCount = 2
	jobs := make(chan queue.CollectionJob, workerCount+1)
	for range workerCount + 1 {
		jobs <- queue.CollectionJob{JobID: uuid.New()}
	}
	close(jobs)

	started := make(chan struct{}, workerCount+1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseWorkers := func() {
		releaseOnce.Do(func() { close(release) })
	}
	defer releaseWorkers()

	processor := processorFunc(func(_ context.Context, _ queue.CollectionJob) error {
		started <- struct{}{}
		<-release
		return nil
	})

	pool := mustNewWorkerPool(t, workerCount, jobs, processor, time.Second)
	done := runWorkerPool(pool, context.Background())

	for range workerCount {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for workers to start")
		}
	}

	select {
	case <-started:
		t.Fatal("more jobs started than the configured worker count")
	case <-time.After(20 * time.Millisecond):
	}

	releaseWorkers()
	waitForWorkerPool(t, done)
}

func TestWorkerPoolAppliesTimeoutToEachJob(t *testing.T) {
	t.Parallel()

	jobs := make(chan queue.CollectionJob, 1)
	jobs <- queue.CollectionJob{JobID: uuid.New()}
	close(jobs)

	processorError := make(chan error, 1)
	processor := processorFunc(func(ctx context.Context, _ queue.CollectionJob) error {
		if _, ok := ctx.Deadline(); !ok {
			processorError <- errors.New("job context has no deadline")
			return nil
		}
		<-ctx.Done()
		processorError <- ctx.Err()
		return ctx.Err()
	})

	pool := mustNewWorkerPool(t, 1, jobs, processor, 20*time.Millisecond)
	waitForWorkerPool(t, runWorkerPool(pool, context.Background()))

	if err := <-processorError; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("processor context error = %v, want context.DeadlineExceeded", err)
	}
}

func TestWorkerPoolContinuesAfterProcessorError(t *testing.T) {
	t.Parallel()

	firstJobID := uuid.New()
	secondJobID := uuid.New()
	jobs := make(chan queue.CollectionJob, 2)
	jobs <- queue.CollectionJob{JobID: firstJobID}
	jobs <- queue.CollectionJob{JobID: secondJobID}
	close(jobs)

	processed := make([]uuid.UUID, 0, 2)
	processor := processorFunc(func(_ context.Context, job queue.CollectionJob) error {
		processed = append(processed, job.JobID)
		if job.JobID == firstJobID {
			return errors.New("collection failed")
		}
		return nil
	})

	pool := mustNewWorkerPool(t, 1, jobs, processor, time.Second)
	waitForWorkerPool(t, runWorkerPool(pool, context.Background()))

	if len(processed) != 2 {
		t.Fatalf("processed %d jobs, want 2", len(processed))
	}
	if processed[0] != firstJobID || processed[1] != secondJobID {
		t.Fatalf("processed jobs = %v, want [%s %s]", processed, firstJobID, secondJobID)
	}
}

func TestWorkerPoolLogsJobOutcomes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		processErr  error
		wantLevel   string
		wantMessage string
		wantEvent   string
	}{
		{
			name:        "success",
			wantLevel:   "INFO",
			wantMessage: "collector job succeeded",
			wantEvent:   "collector.job.succeeded",
		},
		{
			name:        "failure",
			processErr:  errors.New("collection failed"),
			wantLevel:   "ERROR",
			wantMessage: "collector job failed",
			wantEvent:   "collector.job.failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var logs bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
			job := queue.CollectionJob{
				JobID:      uuid.New(),
				DatabaseID: uuid.New(),
				Extensions: []string{"pg_stat_statements", "pgstattuple"},
			}
			jobs := make(chan queue.CollectionJob, 1)
			jobs <- job
			close(jobs)

			pool := mustNewWorkerPoolWithLogger(
				t,
				1,
				jobs,
				processorFunc(func(context.Context, queue.CollectionJob) error { return tt.processErr }),
				time.Second,
				logger,
			)
			waitForWorkerPool(t, runWorkerPool(pool, context.Background()))

			records := decodeLogRecords(t, logs.Bytes())
			if len(records) != 2 {
				t.Fatalf("log record count = %d, want 2", len(records))
			}

			assertJobLogRecord(t, records[0], job, "DEBUG", "collector job started", "collector.event.started")
			assertJobLogRecord(t, records[1], job, tt.wantLevel, tt.wantMessage, tt.wantEvent)
			if _, ok := records[1]["duration_ms"].(float64); !ok {
				t.Fatalf("duration_ms = %#v, want a number", records[1]["duration_ms"])
			}
			if tt.processErr != nil && records[1]["error"] != tt.processErr.Error() {
				t.Fatalf("error = %#v, want %q", records[1]["error"], tt.processErr)
			}
		})
	}
}

func TestWorkerPoolStopsWhenContextIsCanceled(t *testing.T) {
	t.Parallel()

	jobs := make(chan queue.CollectionJob)
	processorCalled := make(chan struct{}, 1)
	pool := mustNewWorkerPool(t, 2, jobs, processorFunc(func(context.Context, queue.CollectionJob) error {
		processorCalled <- struct{}{}
		return nil
	}), time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	done := runWorkerPool(pool, ctx)
	cancel()

	waitForWorkerPool(t, done)
	select {
	case <-processorCalled:
		t.Fatal("processor called without a submitted job")
	default:
	}
}

func TestNewWorkerPoolRejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()

	jobs := make(chan queue.CollectionJob)
	processor := processorFunc(func(context.Context, queue.CollectionJob) error {
		return nil
	})
	logger := discardLogger()

	tests := []struct {
		name        string
		workerCount int
		jobs        <-chan queue.CollectionJob
		processor   JobProcessor
		timeout     time.Duration
		logger      *slog.Logger
		wantError   string
	}{
		{
			name:        "zero workers",
			workerCount: 0,
			jobs:        jobs,
			processor:   processor,
			timeout:     time.Second,
			logger:      logger,
			wantError:   "Worker count must be greater than zero",
		},
		{
			name:        "negative workers",
			workerCount: -1,
			jobs:        jobs,
			processor:   processor,
			timeout:     time.Second,
			logger:      logger,
			wantError:   "Worker count must be greater than zero",
		},
		{
			name:        "zero timeout",
			workerCount: 1,
			jobs:        jobs,
			processor:   processor,
			timeout:     0,
			logger:      logger,
			wantError:   "Timeout must be greater than zero",
		},
		{
			name:        "negative timeout",
			workerCount: 1,
			jobs:        jobs,
			processor:   processor,
			timeout:     -time.Second,
			logger:      logger,
			wantError:   "Timeout must be greater than zero",
		},
		{
			name:        "nil jobs channel",
			workerCount: 1,
			jobs:        nil,
			processor:   processor,
			timeout:     time.Second,
			logger:      logger,
			wantError:   "Jobs queue or processor can't be empty",
		},
		{
			name:        "nil processor",
			workerCount: 1,
			jobs:        jobs,
			processor:   nil,
			timeout:     time.Second,
			logger:      logger,
			wantError:   "Jobs queue or processor can't be empty",
		},
		{
			name:        "nil logger",
			workerCount: 1,
			jobs:        jobs,
			processor:   processor,
			timeout:     time.Second,
			wantError:   "Logger can't be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool, err := NewWorkerPool(
				tt.workerCount,
				tt.jobs,
				tt.processor,
				tt.timeout,
				tt.logger,
			)
			if err == nil {
				t.Fatal("NewWorkerPool returned nil error")
			}
			if err.Error() != tt.wantError {
				t.Fatalf("NewWorkerPool error = %q, want %q", err, tt.wantError)
			}
			if pool != nil {
				t.Fatalf("NewWorkerPool returned pool %#v for invalid configuration", pool)
			}
		})
	}
}

func mustNewWorkerPool(
	t *testing.T,
	workerCount int,
	jobs <-chan queue.CollectionJob,
	processor JobProcessor,
	timeout time.Duration,
) *WorkerPool {
	t.Helper()

	return mustNewWorkerPoolWithLogger(t, workerCount, jobs, processor, timeout, discardLogger())
}

func mustNewWorkerPoolWithLogger(
	t *testing.T,
	workerCount int,
	jobs <-chan queue.CollectionJob,
	processor JobProcessor,
	timeout time.Duration,
	logger *slog.Logger,
) *WorkerPool {
	t.Helper()

	pool, err := NewWorkerPool(workerCount, jobs, processor, timeout, logger)
	if err != nil {
		t.Fatalf("NewWorkerPool: %v", err)
	}
	return pool
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func decodeLogRecords(t *testing.T, data []byte) []map[string]any {
	t.Helper()

	decoder := json.NewDecoder(bytes.NewReader(data))
	var records []map[string]any
	for decoder.More() {
		var record map[string]any
		if err := decoder.Decode(&record); err != nil {
			t.Fatalf("decode log record: %v", err)
		}
		records = append(records, record)
	}
	return records
}

func assertJobLogRecord(
	t *testing.T,
	record map[string]any,
	job queue.CollectionJob,
	wantLevel string,
	wantMessage string,
	wantEvent string,
) {
	t.Helper()

	wantFields := map[string]any{
		"level":       wantLevel,
		"msg":         wantMessage,
		"component":   "collector.worker",
		"worker_id":   float64(1),
		"job_id":      job.JobID.String(),
		"database_id": job.DatabaseID.String(),
		"event":       wantEvent,
	}
	for field, want := range wantFields {
		if got := record[field]; got != want {
			t.Errorf("%s = %#v, want %#v", field, got, want)
		}
	}

	extensions, ok := record["extensions"].([]any)
	if !ok || len(extensions) != len(job.Extensions) {
		t.Fatalf("extensions = %#v, want %v", record["extensions"], job.Extensions)
	}
	for i, want := range job.Extensions {
		if extensions[i] != want {
			t.Errorf("extensions[%d] = %#v, want %q", i, extensions[i], want)
		}
	}
}

func runWorkerPool(pool *WorkerPool, ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		pool.Run(ctx)
		close(done)
	}()
	return done
}

func waitForWorkerPool(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for worker pool to stop")
	}
}
