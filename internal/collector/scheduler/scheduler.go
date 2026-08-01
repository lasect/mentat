package scheduler

import (
	"container/heap"
	"context"
	"fmt"
	"mentat/internal/appdb"
	"mentat/internal/collector/queue"
	"time"

	"github.com/google/uuid"
)

type extensionGroup struct {
	Key        groupKey
	Extensions []string
	NextRunAt  time.Time
}

type groupKey struct {
	DatabaseID      uuid.UUID
	IntervalSeconds int32
}

type Scheduler struct {
	queries  *appdb.Queries
	schedule scheduleHeap
	queue    *queue.Queue
}

func NewScheduler(queue *queue.Queue, queries *appdb.Queries) *Scheduler {
	return &Scheduler{
		queue:    queue,
		queries:  queries,
		schedule: make(scheduleHeap, 0),
	}
}

func (s *Scheduler) InitializeSchedule(ctx context.Context) error {
	row, err := s.queries.ListActiveExtensionsForCollector(ctx)
	if err != nil {
		return fmt.Errorf("list active extensions: %w", err)
	}

	groups := groupExtensions(row)
	s.schedule = make(scheduleHeap, 0, len(groups))

	for _, group := range groups {
		heap.Push(&s.schedule, group)
	}
	heap.Init(&s.schedule)

	return nil
}

func groupExtensions(rows []appdb.ListActiveExtensionsForCollectorRow) []extensionGroup {
	startupTime := time.Now()
	groups := make([]extensionGroup, 0)
	groupIndexes := make(map[groupKey]int)

	for _, row := range rows {
		key := groupKey{
			DatabaseID:      row.DatabaseID,
			IntervalSeconds: row.IntervalSeconds,
		}

		nextRunAt := startupTime
		if row.NextRunAt.Valid {
			nextRunAt = row.NextRunAt.Time
		}

		if index, exists := groupIndexes[key]; exists {
			groups[index].Extensions = append(groups[index].Extensions, row.Extension)
			if nextRunAt.Before(groups[index].NextRunAt) {
				groups[index].NextRunAt = nextRunAt
			}
			continue
		}

		groupIndexes[key] = len(groups)
		groups = append(groups, extensionGroup{
			Key:        key,
			Extensions: []string{row.Extension},
			NextRunAt:  nextRunAt,
		})
	}

	return groups
}
