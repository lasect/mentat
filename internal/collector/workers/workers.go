package workers

import (
	"context"
	"mentat/internal/collector/queue"
	"sync"
	"time"
)

type JobProcessor interface {
	Process(ctx context.Context, job queue.CollectionJob) error
}

type WorkerPool struct {
	workerCount int
	jobs        <-chan queue.CollectionJob
	processor   JobProcessor
	timeout     time.Duration
}

func InitializeWorkerPool(workerCount int, jobs <-chan queue.CollectionJob, processor JobProcessor, timeout time.Duration) *WorkerPool {
	return &WorkerPool{
		workerCount: workerCount,
		jobs:        jobs,
		processor:   processor,
		timeout:     timeout,
	}
}

func (p *WorkerPool) Run(ctx context.Context) {
	var wg sync.WaitGroup

	for i := 0; i < p.workerCount; i++ {
		wg.Add(1)

		wg.Go(func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case job, ok := <-p.jobs:
					if !ok {
						return
					}
					jobCtx, cancel := context.WithTimeout(ctx, p.timeout)
					err := p.processor.Process(jobCtx, job)
					cancel()

					if err != nil {
						//return fmt.Errorf("Error processing job:%s, Worker ID: %s, Database ID: %s, JobID: %s ", err, i, job.DatabaseID, job.JobID )
						return
					}
				}
			}
		})
	}
	wg.Wait()
}
