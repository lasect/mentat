package prober

import (
	"time"

	"github.com/google/uuid"
)

type ProberExtensionSnapshot struct {
	ID             uuid.UUID
	DatabaseID     uuid.UUID
	DatabaseStatus DatabaseStatus
	Reason         Reason
	Readiness      Readiness
	Extensions     ExtensionResult
	StartedAt      time.Time
	CompletedAt    time.Time
}

// Database snapshots use the same outcome as execution so future persistence
// retains event correlation, partial results, and probe-level failures.
type proberDatabaseSnapshot = ProberResult
