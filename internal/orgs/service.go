package orgs

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"tetra/internal/appdb"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	slugInvalidRun = regexp.MustCompile(`[^a-z0-9]+`)
	slugValid      = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)
)

var supportedExtensions = map[string]struct{}{
	"pg_stat_statements": {},
	"pg_stat_monitor":    {},
	"pgstattuple":        {},
	"pg_buffercache":     {},
	"pg_mentat":          {},
}

type Organization struct {
	ID             uuid.UUID `json:"id"`
	Name           string    `json:"name"`
	Slug           string    `json:"slug"`
	Plan           string    `json:"plan"`
	AnalyticsStore string    `json:"analytics_store"`
	Role           string    `json:"role"`
}

type Database struct {
	ID             uuid.UUID `json:"id"`
	OrganizationID uuid.UUID `json:"organization_id"`
	Name           string    `json:"name"`
	Slug           string    `json:"slug"`
	Extensions     []string  `json:"extensions"`
	CreatedAt      time.Time `json:"created_at"`
}

type Service struct {
	pool    *pgxpool.Pool
	queries *appdb.Queries
	cipher  *ConnectionCipher
}

func NewService(pool *pgxpool.Pool, cipher *ConnectionCipher) (*Service, error) {
	if pool == nil || cipher == nil {
		return nil, fmt.Errorf("database and connection cipher are required")
	}
	return &Service{pool: pool, queries: appdb.New(pool), cipher: cipher}, nil
}

func (s *Service) CreateOrganization(ctx context.Context, userID uuid.UUID, name, requestedSlug string) (Organization, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 120 {
		return Organization{}, fmt.Errorf("%w: organization name must be between 1 and 120 characters", ErrInvalidInput)
	}
	slug, err := NormalizeSlug(requestedSlug)
	if requestedSlug == "" {
		slug, err = NormalizeSlug(name)
	}
	if err != nil {
		return Organization{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Organization{}, fmt.Errorf("begin organization creation: %w", err)
	}
	defer tx.Rollback(ctx)
	q := s.queries.WithTx(tx)
	row, err := q.CreateOrganization(ctx, appdb.CreateOrganizationParams{
		ID: uuid.New(), Name: name, Slug: slug,
	})
	if isUniqueViolation(err) {
		return Organization{}, ErrSlugTaken
	}
	if err != nil {
		return Organization{}, fmt.Errorf("create organization: %w", err)
	}
	if _, err := q.CreateOrganizationMembership(ctx, appdb.CreateOrganizationMembershipParams{
		OrganizationID: row.ID, UserID: userID, Role: "owner",
	}); err != nil {
		return Organization{}, fmt.Errorf("create organization owner: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Organization{}, fmt.Errorf("commit organization creation: %w", err)
	}
	return Organization{
		ID: row.ID, Name: row.Name, Slug: row.Slug, Plan: row.Plan,
		AnalyticsStore: row.AnalyticsStore, Role: "owner",
	}, nil
}

func (s *Service) ListOrganizations(ctx context.Context, userID uuid.UUID) ([]Organization, error) {
	rows, err := s.queries.ListOrganizationsForUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list organizations: %w", err)
	}
	result := make([]Organization, 0, len(rows))
	for _, row := range rows {
		result = append(result, Organization{
			ID: row.ID, Name: row.Name, Slug: row.Slug, Plan: row.Plan,
			AnalyticsStore: row.AnalyticsStore, Role: row.Role,
		})
	}
	return result, nil
}

func (s *Service) GetOrganization(ctx context.Context, userID uuid.UUID, slug string) (Organization, error) {
	row, err := s.queries.GetOrganizationBySlugForUser(ctx, appdb.GetOrganizationBySlugForUserParams{
		Slug: slug, UserID: userID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Organization{}, ErrNotFound
	}
	if err != nil {
		return Organization{}, fmt.Errorf("get organization: %w", err)
	}
	return Organization{
		ID: row.ID, Name: row.Name, Slug: row.Slug, Plan: row.Plan,
		AnalyticsStore: row.AnalyticsStore, Role: row.Role,
	}, nil
}

func (s *Service) UpdateAnalyticsStore(ctx context.Context, userID uuid.UUID, slug, store string) (Organization, error) {
	store = strings.ToLower(strings.TrimSpace(store))
	if store != "duckdb" && store != "clickhouse" {
		return Organization{}, ErrInvalidStore
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Organization{}, fmt.Errorf("begin analytics store update: %w", err)
	}
	defer tx.Rollback(ctx)
	q := s.queries.WithTx(tx)
	organization, err := lockOrganization(ctx, q, userID, slug)
	if err != nil {
		return Organization{}, err
	}
	if !canAdmin(organization.Role) {
		return Organization{}, ErrForbidden
	}
	if store == "clickhouse" && organization.Plan == "free" {
		return Organization{}, ErrPaidPlanRequired
	}
	row, err := q.UpdateOrganizationAnalyticsStore(ctx, appdb.UpdateOrganizationAnalyticsStoreParams{
		ID: organization.ID, AnalyticsStore: store,
	})
	if isCheckViolation(err) && store == "clickhouse" {
		return Organization{}, ErrPaidPlanRequired
	}
	if err != nil {
		return Organization{}, fmt.Errorf("update analytics store: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Organization{}, fmt.Errorf("commit analytics store update: %w", err)
	}
	organization.AnalyticsStore = row.AnalyticsStore
	organization.Plan = row.Plan
	return organization, nil
}

func (s *Service) CreateDatabase(ctx context.Context, userID uuid.UUID, orgSlug, name, requestedSlug, connectionString string, extensions []string) (Database, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 120 {
		return Database{}, fmt.Errorf("%w: database name must be between 1 and 120 characters", ErrInvalidInput)
	}
	slug, err := NormalizeSlug(requestedSlug)
	if requestedSlug == "" {
		slug, err = NormalizeSlug(name)
	}
	if err != nil {
		return Database{}, err
	}
	connectionString, err = ValidateConnectionString(connectionString)
	if err != nil {
		return Database{}, err
	}
	extensions, err = NormalizeExtensions(extensions)
	if err != nil {
		return Database{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Database{}, fmt.Errorf("begin database creation: %w", err)
	}
	defer tx.Rollback(ctx)
	q := s.queries.WithTx(tx)
	organization, err := lockOrganization(ctx, q, userID, orgSlug)
	if err != nil {
		return Database{}, err
	}
	if !canAdmin(organization.Role) {
		return Database{}, ErrForbidden
	}
	databaseID := uuid.New()
	aad := connectionAAD(organization.ID, databaseID, s.cipher.keyVersion)
	ciphertext, nonce, keyVersion, err := s.cipher.Encrypt(connectionString, aad)
	if err != nil {
		return Database{}, err
	}
	row, err := q.CreateDatabase(ctx, appdb.CreateDatabaseParams{
		ID: databaseID, OrganizationID: organization.ID, Name: name, Slug: slug,
		ConnectionCiphertext: ciphertext, ConnectionNonce: nonce,
		EncryptionKeyVersion: keyVersion, CreatedBy: userID,
	})
	if isUniqueViolation(err) {
		return Database{}, ErrSlugTaken
	}
	if err != nil {
		return Database{}, fmt.Errorf("create database: %w", err)
	}
	for _, extension := range extensions {
		if err := q.SelectDatabaseExtension(ctx, appdb.SelectDatabaseExtensionParams{
			DatabaseID: row.ID, Extension: extension, SelectedBy: userID,
		}); err != nil {
			return Database{}, fmt.Errorf("select database extension: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Database{}, fmt.Errorf("commit database creation: %w", err)
	}
	return publicDatabase(row, extensions), nil
}

func (s *Service) ListDatabases(ctx context.Context, userID uuid.UUID, orgSlug string) ([]Database, error) {
	organization, err := s.GetOrganization(ctx, userID, orgSlug)
	if err != nil {
		return nil, err
	}
	rows, err := s.queries.ListDatabasesForOrganization(ctx, organization.ID)
	if err != nil {
		return nil, fmt.Errorf("list databases: %w", err)
	}
	result := make([]Database, 0, len(rows))
	for _, row := range rows {
		extensions, err := s.queries.ListDatabaseExtensions(ctx, row.ID)
		if err != nil {
			return nil, fmt.Errorf("list database extensions: %w", err)
		}
		result = append(result, publicDatabase(row, extensions))
	}
	return result, nil
}

func (s *Service) SetDatabaseExtensions(ctx context.Context, userID uuid.UUID, orgSlug, databaseSlug string, requested []string) (Database, error) {
	extensions, err := NormalizeExtensions(requested)
	if err != nil {
		return Database{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Database{}, fmt.Errorf("begin extension update: %w", err)
	}
	defer tx.Rollback(ctx)
	q := s.queries.WithTx(tx)
	organization, err := lockOrganization(ctx, q, userID, orgSlug)
	if err != nil {
		return Database{}, err
	}
	if !canAdmin(organization.Role) {
		return Database{}, ErrForbidden
	}
	database, err := q.GetDatabaseBySlugForOrganization(ctx, appdb.GetDatabaseBySlugForOrganizationParams{
		Slug: databaseSlug, OrganizationID: organization.ID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Database{}, ErrNotFound
	}
	if err != nil {
		return Database{}, fmt.Errorf("get database: %w", err)
	}
	existing, err := q.ListDatabaseExtensions(ctx, database.ID)
	if err != nil {
		return Database{}, fmt.Errorf("list current extensions: %w", err)
	}
	wanted := make(map[string]struct{}, len(extensions))
	for _, extension := range extensions {
		wanted[extension] = struct{}{}
		if err := q.SelectDatabaseExtension(ctx, appdb.SelectDatabaseExtensionParams{
			DatabaseID: database.ID, Extension: extension, SelectedBy: userID,
		}); err != nil {
			return Database{}, fmt.Errorf("select extension: %w", err)
		}
	}
	for _, extension := range existing {
		if _, ok := wanted[extension]; !ok {
			if _, err := q.DeselectDatabaseExtension(ctx, appdb.DeselectDatabaseExtensionParams{
				DatabaseID: database.ID, Extension: extension,
			}); err != nil {
				return Database{}, fmt.Errorf("deselect extension: %w", err)
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Database{}, fmt.Errorf("commit extension update: %w", err)
	}
	return publicDatabase(database, extensions), nil
}

func (s *Service) DeleteDatabase(ctx context.Context, userID uuid.UUID, orgSlug, databaseSlug string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin database deletion: %w", err)
	}
	defer tx.Rollback(ctx)
	q := s.queries.WithTx(tx)
	organization, err := lockOrganization(ctx, q, userID, orgSlug)
	if err != nil {
		return err
	}
	if !canAdmin(organization.Role) {
		return ErrForbidden
	}
	database, err := q.GetDatabaseBySlugForOrganization(ctx, appdb.GetDatabaseBySlugForOrganizationParams{
		Slug: databaseSlug, OrganizationID: organization.ID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("get database for deletion: %w", err)
	}
	count, err := q.DeleteDatabase(ctx, appdb.DeleteDatabaseParams{
		ID: database.ID, OrganizationID: organization.ID,
	})
	if err != nil {
		return fmt.Errorf("delete database: %w", err)
	}
	if count != 1 {
		return ErrNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit database deletion: %w", err)
	}
	return nil
}

func (s *Service) DecryptDatabaseConnection(ctx context.Context, organizationID, databaseID uuid.UUID) (string, error) {
	row, err := s.queries.GetDatabaseForOrganization(ctx, appdb.GetDatabaseForOrganizationParams{
		ID: databaseID, OrganizationID: organizationID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("get encrypted database connection: %w", err)
	}
	return s.cipher.Decrypt(
		row.ConnectionCiphertext,
		row.ConnectionNonce,
		connectionAAD(row.OrganizationID, row.ID, row.EncryptionKeyVersion),
		row.EncryptionKeyVersion,
	)
}

func NormalizeSlug(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	value = slugInvalidRun.ReplaceAllString(value, "-")
	value = strings.Trim(value, "-")
	if value == "" {
		return "", fmt.Errorf("%w: slug must contain letters or numbers", ErrInvalidInput)
	}
	if len(value) < 3 {
		value = strings.Trim(value+"-org", "-")
	}
	if len(value) > 63 {
		value = strings.Trim(value[:63], "-")
	}
	if !slugValid.MatchString(value) {
		return "", fmt.Errorf("%w: slug must contain 3-63 lowercase letters, numbers, or hyphens", ErrInvalidInput)
	}
	return value, nil
}

func ValidateConnectionString(value string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) == 0 || len(value) > 4096 {
		return "", ErrInvalidConnection
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") ||
		parsed.Host == "" || strings.Trim(parsed.Path, "/") == "" {
		return "", ErrInvalidConnection
	}
	if parsed.User == nil {
		return "", ErrInvalidConnection
	}
	return value, nil
}

func NormalizeExtensions(values []string) ([]string, error) {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if _, ok := supportedExtensions[value]; !ok {
			return nil, fmt.Errorf("%w: %s", ErrInvalidExtension, value)
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result, nil
}

func canAdmin(role string) bool {
	return role == "owner" || role == "admin"
}

func lockOrganization(ctx context.Context, q *appdb.Queries, userID uuid.UUID, slug string) (Organization, error) {
	row, err := q.LockOrganizationBySlugForUser(ctx, appdb.LockOrganizationBySlugForUserParams{
		Slug: slug, UserID: userID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Organization{}, ErrNotFound
	}
	if err != nil {
		return Organization{}, fmt.Errorf("lock organization: %w", err)
	}
	return Organization{
		ID: row.ID, Name: row.Name, Slug: row.Slug, Plan: row.Plan,
		AnalyticsStore: row.AnalyticsStore, Role: row.Role,
	}, nil
}

func connectionAAD(organizationID, databaseID uuid.UUID, keyVersion int16) []byte {
	return []byte(fmt.Sprintf("tetra:db-connection:v%d:%s:%s", keyVersion, organizationID, databaseID))
}

func publicDatabase(row appdb.Database, extensions []string) Database {
	return Database{
		ID: row.ID, OrganizationID: row.OrganizationID, Name: row.Name, Slug: row.Slug,
		Extensions: extensions, CreatedAt: row.CreatedAt.Time,
	}
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func isCheckViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23514"
}
