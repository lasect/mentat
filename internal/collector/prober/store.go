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

type proberDatabaseSnapshot struct {
	DatabaseID  uuid.UUID
	Reason      Reason
	Readiness   Readiness
	Extensions  map[ExtensionName]ExtensionResult
	StartedAt   time.Time
	CompletedAt time.Time
}
