package scraper

import (
	"context"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresBatchLifecycle(t *testing.T) {
	url := os.Getenv("MENTAT_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("MENTAT_TEST_DATABASE_URL is not set")
	}
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	s := &ScraperProcess{getDatabase: func(uuid.UUID) (batchSender, error) { return pool, nil }}
	job := testJob(t, "SELECT 42::int4 AS number, NULL::text AS absent, decode('0102','hex') AS bytes, ARRAY[1,2] AS numbers, '{\"ok\":true}'::jsonb AS document", "SELECT 'empty'::text AS value WHERE false")
	result, err := s.Scrape(t.Context(), job)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Results) != 2 || len(result.Results[1].Columns) != 1 || len(result.Results[1].Rows) != 0 {
		t.Fatalf("bad result sets: %+v", result)
	}
	if result.Results[0].Columns[0].Name != "number" || result.Results[0].Columns[0].TypeOID != 23 {
		t.Fatal("missing column metadata")
	}
	row := result.Results[0].Rows[0]
	if row[0] != int32(42) || row[1] != nil || !reflect.DeepEqual(row[2], []byte{1, 2}) {
		t.Fatalf("bad values: %v", row)
	}
	// Reuse the sole connection before checking that decoded values remain owned.
	if _, err := pool.Exec(t.Context(), "SELECT repeat('x',100000)"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(row[2], []byte{1, 2}) || !reflect.DeepEqual(row[3], []any{int32(1), int32(2)}) || !reflect.DeepEqual(row[4], map[string]any{"ok": true}) {
		t.Fatalf("values changed after reuse: %v", row)
	}
	failed, err := s.Scrape(t.Context(), testJob(t, "SELECT 1", "SELECT 1 / 0", "SELECT 3"))
	if err == nil || !reflect.DeepEqual(failed, CollectionResult{}) {
		t.Fatalf("partial result escaped: %+v, %v", failed, err)
	}
	if err := pool.Ping(t.Context()); err != nil {
		t.Fatalf("connection unusable after failure: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	failed, err = s.Scrape(ctx, testJob(t, "SELECT pg_sleep(5)"))
	if err == nil || !reflect.DeepEqual(failed, CollectionResult{}) {
		t.Fatalf("cancellation failed: %+v, %v", failed, err)
	}
	checkCtx, checkCancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer checkCancel()
	if err := pool.Ping(checkCtx); err != nil {
		t.Fatalf("pool unusable after cancellation: %v", err)
	}
}
