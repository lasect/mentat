package queue

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestQueueSubmitAndReceive(t *testing.T) {
	t.Parallel()

	q := NewQueue(1)
	job := CollectionJob{
		JobID:           uuid.New(),
		DatabaseID:      uuid.New(),
		Extensions:      []string{"pg_stat_statements"},
		IntervalSeconds: 30,
		ScheduledAt:     time.Now(),
	}

	if err := q.Submit(context.Background(), job); err != nil {
		t.Fatalf("submit job: %v", err)
	}

	received := <-q.Jobs()
	if received.JobID != job.JobID {
		t.Fatalf("received job %s, want %s", received.JobID, job.JobID)
	}
}

func TestQueueSubmitReturnsWhenContextIsCanceled(t *testing.T) {
	t.Parallel()

	q := NewQueue(1)
	if err := q.Submit(context.Background(), CollectionJob{JobID: uuid.New()}); err != nil {
		t.Fatalf("fill queue: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := q.Submit(ctx, CollectionJob{JobID: uuid.New()})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("submit error = %v, want context.Canceled", err)
	}
}

func TestQueueJobsAreConsumedOnceAcrossWorkers(t *testing.T) {
	t.Parallel()

	const (
		workerCount = 3
		jobCount    = 20
	)

	q := NewQueue(jobCount)
	jobIDs := make([]uuid.UUID, 0, jobCount)
	for range jobCount {
		id := uuid.New()
		jobIDs = append(jobIDs, id)
		if err := q.Submit(context.Background(), CollectionJob{JobID: id}); err != nil {
			t.Fatalf("submit job: %v", err)
		}
	}
	q.CloseQueue()

	received := make(chan uuid.UUID, jobCount)
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for job := range q.Jobs() {
				received <- job.JobID
			}
		}()
	}
	workers.Wait()
	close(received)

	counts := make(map[uuid.UUID]int, jobCount)
	for id := range received {
		counts[id]++
	}
	for _, id := range jobIDs {
		if counts[id] != 1 {
			t.Errorf("job %s consumed %d times, want once", id, counts[id])
		}
	}
}

func TestQueueCloseClosesJobsChannel(t *testing.T) {
	t.Parallel()

	q := NewQueue(1)
	q.CloseQueue()

	if _, ok := <-q.Jobs(); ok {
		t.Fatal("jobs channel is open after CloseQueue")
	}
}
