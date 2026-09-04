package prober

import (
	"mentat/internal/clientdb"
	"time"

	"github.com/google/uuid"
)

type DatabaseStatus string

const (
	DatabaseReachable   DatabaseStatus = "reachable"
	DatabaseUnavailable DatabaseStatus = "unavailable"
)

type Reason string

const (
	ReasonRegistration  Reason = "registration"
	ReasonConfigChanged Reason = "config_changed"
	ReasonStartup       Reason = "startup"
	ReasonErrors        Reason = "collector_errors"
	ReasonManual        Reason = "manual"
)

type Readiness string

const (
	ReadinessUnknown           Readiness = "unknown"
	ReadinessReady             Readiness = "ready"
	ReadinessMissingDependency Readiness = "missing_dependency"
	ReadinessPermissionDenied  Readiness = "permission_denied"
	ReadinessUnsupported       Readiness = "unsupported"
	ReadinessUnavailable       Readiness = "unavailable"
)

type prober struct {
	registry *ProberRegistery
	pool     *clientdb.PoolManager
	store    *probeStore
}

type Request struct {
	DatabaseID     uuid.UUID
	ExtensionNames []ExtensionName
	Reason         Reason
	Priority       int
}

type ExtensionResult struct {
	Name         ExtensionName
	Readiness    Readiness
	ErrorCode    string
	ErrorMessage string
}

type ProberResult struct {
	DatabaseID  uuid.UUID
	Extensions  map[ExtensionName]ExtensionResult
	StartedAt   time.Time
	CompletedAt time.Time
}

type ProberExtensionSnapshot struct {
	ID             uuid.UUID
	DatabaseID     uuid.UUID
	DatabaseStatus DatabaseStatus
	Extensions     map[ExtensionName]ExtensionResult
	StartedAt      time.Time
	CompletedAt    time.Time
}

func initializeProbe(registery *ProberRegistery, pool *clientdb.PoolManager) *prober {
	return &prober{
		registry: registery,
		pool:     pool,
	}
}

func (p *prober) ProbeNow() {}
