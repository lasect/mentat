package workers

import (
	"context"
	"fmt"
	"log/slog"
	"mentat/internal/collector/queue"
	"sync"
	"time"
)

type JobProcessor interface {
	Process(ctx context.Context, job queue.CollectionJob) error
}

type WorkerPool struct {
	logger      *slog.Logger
	workerCount int
	jobs        <-chan queue.CollectionJob
	processor   JobProcessor
	timeout     time.Duration
}

func NewWorkerPool(workerCount int, jobs <-chan queue.CollectionJob, processor JobProcessor, timeout time.Duration, logger *slog.Logger) (*WorkerPool, error) {
	if workerCount <= 0 {
		return nil, fmt.Errorf("Worker count must be greater than zero")
	}
	if timeout <= 0 {
		return nil, fmt.Errorf("Timeout must be greater than zero")
	}
	if processor == nil || jobs == nil {
		return nil, fmt.Errorf("Jobs queue or processor can't be empty")
	}
	if logger == nil {
		return nil, fmt.Errorf("Logger can't be empty")
	}

	return &WorkerPool{
		logger: logger.With(
			"component", "collector.worker",
		),
		workerCount: workerCount,
		jobs:        jobs,
		processor:   processor,
		timeout:     timeout,
	}, nil
}

func (p *WorkerPool) Run(ctx context.Context) {
	var wg sync.WaitGroup

	for i := 0; i < p.workerCount; i++ {
		workerID := i + 1

		wg.Go(func() {
			p.runWorker(ctx, workerID)
		})
	}
	wg.Wait()
}

func (p *WorkerPool) runWorker(ctx context.Context, workerID int) {
	workerLogger := p.logger.With("worker_id", workerID)
	for {
		select {
		case <-ctx.Done():
			return
		case job, ok := <-p.jobs:
			if !ok {
				return
			}
			p.processJob(ctx, workerLogger, job)

		}
	}
}

func (p *WorkerPool) processJob(ctx context.Context, workerLogger *slog.Logger, job queue.CollectionJob) {

	jobContext, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	jobLogger := workerLogger.With(
		"job_id", job.JobID,
		"database_id", job.DatabaseID,
		"extensions", job.Extensions,
	)

	startedAt := time.Now()

	jobLogger.DebugContext(jobContext, "collector job started", "event", "collector.event.started")

	err := p.processor.Process(jobContext, job)
	duration := time.Since(startedAt)
	if err != nil {
		jobLogger.ErrorContext(
			jobContext,
			"collector job failed",
			"event", "collector.job.failed",
			"duration_ms", duration.Milliseconds(),
			"error", err,
		)
		return
	}
	jobLogger.InfoContext(
		jobContext,
		"collector job succeeded",
		"event", "collector.job.succeeded",
		"duration_ms", duration.Milliseconds(),
	)
}
