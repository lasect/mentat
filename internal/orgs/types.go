package orgs

import (
	"crypto/cipher"
	"mentat/internal/appdb"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Organization struct {
	ID             uuid.UUID `json:"id"`
	Name           string    `json:"name"`
	Slug           string    `json:"slug"`
	Plan           string    `json:"plan"`
	AnalyticsStore string    `json:"analytics_store"`
	Role           string    `json:"role"`
}

type Database struct {
	ID             uuid.UUID      `json:"id"`
	OrganizationID uuid.UUID      `json:"organization_id"`
	Name           string         `json:"name"`
	Slug           string         `json:"slug"`
	Extensions     map[string]int `json:"extensions"`
	CreatedAt      time.Time      `json:"created_at"`
}

type Service struct {
	pool    *pgxpool.Pool
	queries *appdb.Queries
	cipher  *ConnectionCipher
}

type ConnectionCipher struct {
	aead       cipher.AEAD
	keyVersion int16
}
