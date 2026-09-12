# Collection plans and scraper

Register trusted, read-only SQL through `collection.NewBuilder([]collection.QuerySpec{...})`.
Each definition has an extension, a result key unique within that extension, and one
SQL statement returning one result set. Multiple queries per extension are supported.
Definitions must not contain transaction control. The builder validates metadata,
not SQL syntax. Production extension queries are intentionally not registered yet.

Pass the builder to `scheduler.NewScheduler(jobQueue, appQueries, builder, logger)`.
Call `InitializeSchedule` before starting the scheduler. Initialization builds all
plans before replacing the schedule; initialization must not run concurrently with
scheduler startup or execution. Each recurring job references its group's immutable
plan. No separate cache, refresh loop, or persistence is involved.

Create the processor with `scraper.NewScraper(poolManager, sink)`, where the sink
implements `Store(context.Context, scraper.CollectionResult) error`. Supply that
processor to the existing worker pool. The pool manager must already contain the
client databases. The worker context controls the whole job deadline, including sink
delivery.

`Scrape(ctx, job)` returns raw results without calling the sink. `Process(ctx, job)`
calls `Scrape`, then sends the complete result to the sink once. Results retain query
identity, column names/type OIDs, decoded Go values (including NULL as nil), and job
timestamps. Standard pgx codecs return owned values; custom codecs must honor that
ownership contract. The results are not a JSON serialization contract.

Every execution creates a fresh pgx batch. SQL, decode, cancellation, or batch-close
errors discard the entire result. There are no automatic retries, sequential
fallbacks, partial deliveries, or collection-state updates. A sink error fails the
job; the scraper cannot roll back side effects performed by the sink.

Queries currently take no runtime arguments. Production SQL, version compatibility,
filters, data-volume bounds, transformations, and storage are separate work. Results
are buffered for the complete group, so queries must bound their output before use
in production.

Validation:

```sh
go test -race ./internal/collector/...
# Set MENTAT_TEST_DATABASE_URL to a test PostgreSQL connection string first:
go test ./internal/collector/scraper -run TestPostgresBatchLifecycle -v -count=1
```

The PostgreSQL test uses only SELECT statements and needs no extension installation
or schema changes. Without the environment variable it is skipped.
