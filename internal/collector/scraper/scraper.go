// Package scraper executes collection plans without knowing extension schemas.
package scraper

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"mentat/internal/clientdb"
	"mentat/internal/collector/queue"
)

// Result limits apply to every scrape, including direct calls to Scrape.
const (
	MaxRowsPerQuery = 10_000
	MaxRowsPerJob   = 50_000
)

// ErrResultLimit indicates that a query or job exceeded its row budget.
var ErrResultLimit = errors.New("collection result row limit exceeded")

type Column struct {
	Name    string
	TypeOID uint32
}

type QueryResult struct {
	Extension string
	ResultKey string
	Columns   []Column
	Rows      [][]any
}

// CollectionResult owns decoded values, not pgx rows or connection buffers.
// Results are buffered for a complete job within the enforced row limits.
type CollectionResult struct {
	JobID       uuid.UUID
	DatabaseID  uuid.UUID
	ScheduledAt time.Time
	StartedAt   time.Time
	CompletedAt time.Time
	Results     []QueryResult
}

// ResultSink receives a complete successful scrape. Persistence is supplied by
// the caller; a sink error fails Process without automatically retrying delivery.
type ResultSink interface {
	Store(context.Context, CollectionResult) error
}

type batchSender interface {
	SendBatch(context.Context, *pgx.Batch) pgx.BatchResults
}

type ScraperProcess struct {
	getDatabase func(uuid.UUID) (batchSender, error)
	sink        ResultSink
}

// NewScraper creates a processor that delivers complete results to sink.
func NewScraper(pool *clientdb.PoolManager, sink ResultSink) (*ScraperProcess, error) {
	if pool == nil || sink == nil {
		return nil, fmt.Errorf("scraper requires pool manager and result sink")
	}
	return &ScraperProcess{
		getDatabase: func(id uuid.UUID) (batchSender, error) { return pool.Get(id) },
		sink:        sink,
	}, nil
}

// Process scrapes a job and delivers it once, only if collection succeeds.
func (s *ScraperProcess) Process(ctx context.Context, job queue.CollectionJob) error {
	if s == nil || s.sink == nil {
		return fmt.Errorf("scraper result sink is not configured")
	}
	result, err := s.Scrape(ctx, job)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.sink.Store(ctx, result); err != nil {
		return fmt.Errorf("store collection result: %w", err)
	}
	return nil
}

// Scrape executes the entire plan as one batch. A failure discards every result.
// The caller owns the deadline. No retries or partial results are produced.
func (s *ScraperProcess) Scrape(ctx context.Context, job queue.CollectionJob) (result CollectionResult, err error) {
	if job.Plan.Len() == 0 {
		return CollectionResult{}, fmt.Errorf("collection job requires a nonempty plan")
	}
	if err := ctx.Err(); err != nil {
		return CollectionResult{}, err
	}
	if s == nil || s.getDatabase == nil {
		return CollectionResult{}, fmt.Errorf("scraper database lookup is not configured")
	}
	result = CollectionResult{JobID: job.JobID, DatabaseID: job.DatabaseID, ScheduledAt: job.ScheduledAt, StartedAt: time.Now()}
	db, err := s.getDatabase(job.DatabaseID)
	if err != nil {
		return CollectionResult{}, fmt.Errorf("get client database %s: %w", job.DatabaseID, err)
	}
	batch := &pgx.Batch{}
	for i := 0; i < job.Plan.Len(); i++ {
		batch.Queue(job.Plan.Query(i).SQL)
	}
	results := db.SendBatch(ctx, batch)
	defer func() {
		if closeErr := results.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close collection batch: %w", closeErr))
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			err = errors.Join(err, ctxErr)
		}
		if err != nil {
			result = CollectionResult{}
		} else {
			result.CompletedAt = time.Now()
		}
	}()
	remainingRows := MaxRowsPerJob
	for i := 0; i < job.Plan.Len(); i++ {
		query := job.Plan.Query(i)
		rows, queryErr := results.Query()
		if queryErr != nil {
			return CollectionResult{}, fmt.Errorf("collect %s/%s: %w", query.Extension, query.ResultKey, queryErr)
		}
		collected, readErr := readRows(rows, &remainingRows)
		if readErr != nil {
			return CollectionResult{}, fmt.Errorf("decode %s/%s: %w", query.Extension, query.ResultKey, readErr)
		}
		collected.Extension, collected.ResultKey = query.Extension, query.ResultKey
		result.Results = append(result.Results, collected)
	}
	return result, nil
}

// readRows owns decoded rows and consumes the job budget before decoding each row.
func readRows(rows pgx.Rows, remainingRows *int) (QueryResult, error) {
	defer rows.Close()
	result := QueryResult{Rows: make([][]any, 0)}
	for _, field := range rows.FieldDescriptions() {
		result.Columns = append(result.Columns, Column{Name: field.Name, TypeOID: field.DataTypeOID})
	}
	for rows.Next() {
		if len(result.Rows) >= MaxRowsPerQuery {
			return QueryResult{}, fmt.Errorf("%w: query maximum is %d", ErrResultLimit, MaxRowsPerQuery)
		}
		if *remainingRows == 0 {
			return QueryResult{}, fmt.Errorf("%w: job maximum is %d", ErrResultLimit, MaxRowsPerJob)
		}
		*remainingRows--
		// Values decodes into owned Go values with pgx's standard codecs. Do not use
		// RawValues, whose buffers are only valid until the next Next call.
		values, err := rows.Values()
		if err != nil {
			return QueryResult{}, err
		}
		result.Rows = append(result.Rows, values)
	}
	if err := rows.Err(); err != nil {
		return QueryResult{}, err
	}
	return result, nil
}
