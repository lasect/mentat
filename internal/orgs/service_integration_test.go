package orgs

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"tetra/internal/appdb"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestOrganizationDatabaseLifecycle(t *testing.T) {
	databaseURL := os.Getenv("TETRA_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TETRA_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	lock, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	if _, err := lock.Exec(ctx, "SELECT pg_advisory_lock(746387214)"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := lock.Exec(context.Background(), "SELECT pg_advisory_unlock(746387214)"); err != nil {
			t.Errorf("release integration-test lock: %v", err)
		}
	}()
	if _, err := pool.Exec(ctx, "TRUNCATE database_extensions, databases, organization_memberships, organizations, refresh_sessions, oauth_identities, password_credentials, users CASCADE"); err != nil {
		t.Fatal(err)
	}
	ownerID := uuid.New()
	secondUserID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, email) VALUES ($1, 'owner@example.com'), ($2, 'member@example.com')
	`, ownerID, secondUserID); err != nil {
		t.Fatal(err)
	}
	cipher, err := NewConnectionCipher(bytes.Repeat([]byte{0x24}, 32), 1)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(pool, cipher)
	if err != nil {
		t.Fatal(err)
	}

	organization, err := service.CreateOrganization(ctx, ownerID, "Acme Systems", "")
	if err != nil {
		t.Fatal(err)
	}
	if organization.Slug != "acme-systems" || organization.Plan != "free" ||
		organization.AnalyticsStore != "duckdb" || organization.Role != "owner" {
		t.Fatalf("organization = %#v", organization)
	}
	if _, err := service.CreateOrganization(ctx, ownerID, "Another Acme", "acme-systems"); !errors.Is(err, ErrSlugTaken) {
		t.Fatalf("duplicate slug error = %v", err)
	}
	if _, err := service.UpdateAnalyticsStore(ctx, ownerID, organization.Slug, "clickhouse"); !errors.Is(err, ErrPaidPlanRequired) {
		t.Fatalf("free ClickHouse error = %v", err)
	}

	var membershipErr *pgconn.PgError
	_, err = pool.Exec(ctx, `
		INSERT INTO organization_memberships (organization_id, user_id, role)
		VALUES ($1, $2, 'member')
	`, organization.ID, secondUserID)
	if !errors.As(err, &membershipErr) || membershipErr.ConstraintName != "free_organization_single_member" {
		t.Fatalf("second Free member error = %v", err)
	}

	database, err := service.CreateDatabase(
		ctx, ownerID, organization.Slug, "Production", "",
		"postgres://tetra:secret@postgres.example:5432/production",
		[]string{"pg_stat_statements", "pgstattuple"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if database.Slug != "production" || len(database.Extensions) != 2 {
		t.Fatalf("database = %#v", database)
	}
	connection, err := service.DecryptDatabaseConnection(ctx, organization.ID, database.ID)
	if err != nil {
		t.Fatal(err)
	}
	if connection != "postgres://tetra:secret@postgres.example:5432/production" {
		t.Fatalf("decrypted connection = %q", connection)
	}
	updated, err := service.SetDatabaseExtensions(
		ctx, ownerID, organization.Slug, database.Slug,
		[]string{"pg_stat_monitor", "pg_mentat"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Extensions) != 2 || updated.Extensions[0] != "pg_mentat" || updated.Extensions[1] != "pg_stat_monitor" {
		t.Fatalf("updated extensions = %#v", updated.Extensions)
	}
	databases, err := service.ListDatabases(ctx, ownerID, organization.Slug)
	if err != nil || len(databases) != 1 {
		t.Fatalf("ListDatabases() = %#v, %v", databases, err)
	}

	if _, err := pool.Exec(ctx, "UPDATE organizations SET plan = 'pro' WHERE id = $1", organization.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO organization_memberships (organization_id, user_id, role)
		VALUES ($1, $2, 'member')
	`, organization.ID, secondUserID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateDatabase(
		ctx, secondUserID, organization.Slug, "Forbidden", "",
		"postgres://tetra:secret@postgres.example/forbidden", nil,
	); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member database creation error = %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE organization_memberships SET role = 'admin'
		WHERE organization_id = $1 AND user_id = $2
	`, organization.ID, secondUserID); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	q := appdb.New(tx)
	if _, err := q.LockOrganizationBySlugForUser(ctx, appdb.LockOrganizationBySlugForUserParams{
		Slug: organization.Slug, UserID: secondUserID,
	}); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	blockedCtx, cancelBlocked := context.WithTimeout(ctx, 100*time.Millisecond)
	_, blockedErr := pool.Exec(blockedCtx, `
		DELETE FROM organization_memberships
		WHERE organization_id = $1 AND user_id = $2
	`, organization.ID, secondUserID)
	cancelBlocked()
	if !errors.Is(blockedErr, context.DeadlineExceeded) {
		_ = tx.Rollback(ctx)
		t.Fatalf("concurrent membership revocation error = %v", blockedErr)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	organization, err = service.UpdateAnalyticsStore(ctx, ownerID, organization.Slug, "clickhouse")
	if err != nil {
		t.Fatal(err)
	}
	if organization.AnalyticsStore != "clickhouse" {
		t.Fatalf("analytics store = %q", organization.AnalyticsStore)
	}
	if err := service.DeleteDatabase(ctx, ownerID, organization.Slug, database.Slug); err != nil {
		t.Fatal(err)
	}
	databases, err = service.ListDatabases(ctx, ownerID, organization.Slug)
	if err != nil || len(databases) != 0 {
		t.Fatalf("databases after delete = %#v, %v", databases, err)
	}
}
