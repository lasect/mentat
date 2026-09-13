package collection

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestDefaultQueriesPostgres(t *testing.T) {
	url := os.Getenv("MENTAT_EXTENSION_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("MENTAT_EXTENSION_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	builder, err := NewDefaultBuilder()
	if err != nil {
		t.Fatal(err)
	}
	plan, err := builder.Build([]string{"pg_stat_monitor", "pg_wait_sampling", "pg_stat_kcache", "pg_qualstats", "pgsentinel"})
	if err != nil {
		t.Fatal(err)
	}
	for i := range plan.Len() {
		q := plan.Query(i)
		t.Run(q.Extension, func(t *testing.T) {
			rows, err := conn.Query(ctx, q.SQL)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			count := 0
			for rows.Next() {
				if _, err := rows.Values(); err != nil {
					t.Fatal(err)
				}
				count++
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if count > 1000 {
				t.Fatalf("returned %d rows, limit is 1000", count)
			}
		})
	}
}
