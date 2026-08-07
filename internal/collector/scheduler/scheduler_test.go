package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"mentat/internal/collector/queue"

	"github.com/google/uuid"
)

func TestStartSchedulerRunsEarliestGroupAndReschedulesIt(t *testing.T) {
	t.Parallel()

	now := time.Now()
	earliestDatabaseID := uuid.New()
	laterDatabaseID := uuid.New()
	jobQueue := queue.NewQueue(2)
	s := NewScheduler(jobQueue, nil)
	s.schedule = scheduleHeap{
		{
			Key:        groupKey{DatabaseID: laterDatabaseID, IntervalSeconds: 60},
			Extensions: []string{"pgstattuple"},
			NextRunAt:  now.Add(time.Hour),
		},
		{
			Key:        groupKey{DatabaseID: earliestDatabaseID, IntervalSeconds: 1},
			Extensions: []string{"pg_stat_statements", "pg_stat_monitor"},
			NextRunAt:  now.Add(-100 * time.Millisecond),
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := runScheduler(s, ctx)

	job := receiveJob(t, jobQueue.Jobs())
	if job.DatabaseID != earliestDatabaseID {
		t.Fatalf("database ID = %s, want %s", job.DatabaseID, earliestDatabaseID)
	}
	if job.IntervalSeconds != 1 {
		t.Fatalf("interval = %d, want 1", job.IntervalSeconds)
	}
	if len(job.Extensions) != 2 || job.Extensions[0] != "pg_stat_statements" || job.Extensions[1] != "pg_stat_monitor" {
		t.Fatalf("extensions = %v, want [pg_stat_statements pg_stat_monitor]", job.Extensions)
	}
	if !job.ScheduledAt.Equal(now.Add(-100 * time.Millisecond)) {
		t.Fatalf("scheduled at = %s, want %s", job.ScheduledAt, now.Add(-100*time.Millisecond))
	}
	if job.JobID == uuid.Nil {
		t.Fatal("job ID is nil")
	}

	recurringJob := receiveJob(t, jobQueue.Jobs())
	if recurringJob.DatabaseID != earliestDatabaseID {
		t.Fatalf("recurring database ID = %s, want %s", recurringJob.DatabaseID, earliestDatabaseID)
	}
	if !recurringJob.ScheduledAt.After(job.ScheduledAt) {
		t.Fatalf("recurring scheduled time = %s, want after %s", recurringJob.ScheduledAt, job.ScheduledAt)
	}

	cancel()
	if err := waitForScheduler(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("scheduler error = %v, want context.Canceled", err)
	}
}

func TestStartSchedulerWaitsUntilNextRun(t *testing.T) {
	t.Parallel()

	jobQueue := queue.NewQueue(1)
	s := NewScheduler(jobQueue, nil)
	s.schedule = scheduleHeap{{
		Key:        groupKey{DatabaseID: uuid.New(), IntervalSeconds: 60},
		Extensions: []string{"pg_stat_statements"},
		NextRunAt:  time.Now().Add(100 * time.Millisecond),
	}}

	ctx, cancel := context.WithCancel(context.Background())
	done := runScheduler(s, ctx)

	select {
	case job := <-jobQueue.Jobs():
		t.Fatalf("received job before it was due: %+v", job)
	case <-time.After(20 * time.Millisecond):
	}

	receiveJob(t, jobQueue.Jobs())
	cancel()
	if err := waitForScheduler(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("scheduler error = %v, want context.Canceled", err)
	}
}

func TestStartSchedulerReturnsErrorForEmptySchedule(t *testing.T) {
	t.Parallel()

	s := NewScheduler(queue.NewQueue(1), nil)
	err := s.StartScheduler(context.Background())
	if err == nil || err.Error() != "heap is empty" {
		t.Fatalf("scheduler error = %v, want heap is empty", err)
	}
}

func TestStartSchedulerCancelsWhileWaiting(t *testing.T) {
	t.Parallel()

	s := NewScheduler(queue.NewQueue(1), nil)
	s.schedule = scheduleHeap{{
		Key:        groupKey{DatabaseID: uuid.New(), IntervalSeconds: 60},
		Extensions: []string{"pg_stat_statements"},
		NextRunAt:  time.Now().Add(time.Hour),
	}}

	ctx, cancel := context.WithCancel(context.Background())
	done := runScheduler(s, ctx)
	cancel()

	if err := waitForScheduler(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("scheduler error = %v, want context.Canceled", err)
	}
}

func TestStartSchedulerCancelsBlockedSubmission(t *testing.T) {
	t.Parallel()

	jobQueue := queue.NewQueue(0)
	s := NewScheduler(jobQueue, nil)
	s.schedule = scheduleHeap{{
		Key:        groupKey{DatabaseID: uuid.New(), IntervalSeconds: 60},
		Extensions: []string{"pg_stat_statements"},
		NextRunAt:  time.Now().Add(-time.Second),
	}}

	ctx, cancel := context.WithCancel(context.Background())
	done := runScheduler(s, ctx)
	cancel()

	if err := waitForScheduler(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("scheduler error = %v, want context.Canceled", err)
	}
}

func TestNextRunAt(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		group    extensionGroup
		expected time.Time
	}{
		{
			name: "next interval is still in the future",
			group: extensionGroup{
				Key:       groupKey{IntervalSeconds: 30},
				NextRunAt: now.Add(-10 * time.Second),
			},
			expected: now.Add(20 * time.Second),
		},
		{
			name: "missed intervals are skipped",
			group: extensionGroup{
				Key:       groupKey{IntervalSeconds: 30},
				NextRunAt: now.Add(-100 * time.Second),
			},
			expected: now.Add(20 * time.Second),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := nextRunAt(tt.group, now); !got.Equal(tt.expected) {
				t.Fatalf("next run = %s, want %s", got, tt.expected)
			}
		})
	}
}

func runScheduler(s *Scheduler, ctx context.Context) <-chan error {
	done := make(chan error, 1)
	go func() {
		done <- s.StartScheduler(ctx)
	}()
	return done
}

func receiveJob(t *testing.T, jobs <-chan queue.CollectionJob) queue.CollectionJob {
	t.Helper()
	select {
	case job := <-jobs:
		return job
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for collection job")
		return queue.CollectionJob{}
	}
}

func waitForScheduler(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for scheduler to stop")
		return nil
	}
}
