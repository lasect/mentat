package queue

import (
	"context"
	"mentat/internal/collector/collection"
	"time"

	"github.com/google/uuid"
)

type CollectionJob struct {
	Plan            *collection.Plan
	JobID           uuid.UUID
	DatabaseID      uuid.UUID
	Extensions      []string
	IntervalSeconds int32
	ScheduledAt     time.Time
}

type Queue struct {
	jobs chan CollectionJob
}

// NewQueue creates a collection queue with the given capacity.
func NewQueue(capacity int) *Queue {
	return &Queue{
		jobs: make(chan CollectionJob, capacity),
	}
}

// Jobs exposes queued jobs to workers.
func (q *Queue) Jobs() <-chan CollectionJob {
	return q.jobs
}

// Submit waits for queue capacity or context cancellation.
func (q *Queue) Submit(ctx context.Context, job CollectionJob) error {
	select {
	case q.jobs <- job:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// CloseQueue closes the job stream after all submitters have stopped.
func (q *Queue) CloseQueue() {
	close(q.jobs)
}
