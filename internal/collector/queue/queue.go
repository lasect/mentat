package queue

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type CollectionJob struct {
	JobID           uuid.UUID
	DatabaseID      uuid.UUID
	Extensions      []string
	IntervalSeconds int32
	ScheduledAt     time.Time
}

type Queue struct {
	jobs chan CollectionJob
}

func NewQueue(capacity int) *Queue {
	return &Queue{
		jobs: make(chan CollectionJob, capacity),
	}
}

func (q *Queue) Jobs() <-chan CollectionJob {
	return q.jobs
}

func (q *Queue) Submit(ctx context.Context, job CollectionJob) error {
	select {
	case q.jobs <- job:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (q *Queue) CloseQueue() {
	close(q.jobs)
}
