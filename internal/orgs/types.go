package orgs

import (
	"crypto/cipher"
	"time"

	"github.com/google/uuid"
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
	ID             uuid.UUID                    `json:"id"`
	OrganizationID uuid.UUID                    `json:"organization_id"`
	Name           string                       `json:"name"`
	Slug           string                       `json:"slug"`
	Extensions     map[string]DatabaseExtension `json:"extensions"`
	CreatedAt      time.Time                    `json:"created_at"`
}

type DatabaseExtension struct {
	IntervalSeconds int  `json:"interval_seconds"`
	IsActive        bool `json:"is_active"`
}

type ConnectionCipher struct {
	aead       cipher.AEAD
	keyVersion int16
}
