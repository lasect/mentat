package workers

import (
	"context"
	"errors"
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

	pool := InitializeWorkerPool(3, jobs, processor, time.Second)
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

	pool := InitializeWorkerPool(workerCount, jobs, processor, time.Second)
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

	pool := InitializeWorkerPool(1, jobs, processor, 20*time.Millisecond)
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

	pool := InitializeWorkerPool(1, jobs, processor, time.Second)
	waitForWorkerPool(t, runWorkerPool(pool, context.Background()))

	if len(processed) != 2 {
		t.Fatalf("processed %d jobs, want 2", len(processed))
	}
	if processed[0] != firstJobID || processed[1] != secondJobID {
		t.Fatalf("processed jobs = %v, want [%s %s]", processed, firstJobID, secondJobID)
	}
}

func TestWorkerPoolStopsWhenContextIsCanceled(t *testing.T) {
	t.Parallel()

	jobs := make(chan queue.CollectionJob)
	processorCalled := make(chan struct{}, 1)
	pool := InitializeWorkerPool(2, jobs, processorFunc(func(context.Context, queue.CollectionJob) error {
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
