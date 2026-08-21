package prober

import (
	"mentat/internal/clientdb"
	"time"

	"github.com/google/uuid"
)

type prober struct {
	registry *ProberRegistery
	pool     *clientdb.PoolManager
	store    *probeStore
}

type Request struct {
	DatabaseID   uuid.UUID
	Capabilities []string
	Generation   int64
	Reason       Reason
	Priority     int
}

type Readiness string

const (
	ReadinessUnknown           Readiness = "unknown"
	ReadinessReady             Readiness = "ready"
	ReadinessMissingDependency Readiness = "missing_dependency"
	ReadinessPermissionDenied  Readiness = "permission_denied"
	ReadinessUnsupported       Readiness = "unsupported"
	ReadinessUnavailable       Readiness = "unavailable"
)

type CollectorResult struct {
	Collector        CollectorName
	Readiness        Readiness
	InstalledVersion string
	ErrorCode        string
	ErrorMessage     string
}

type DatabaseStatus string

const (
	DatabaseReachable   DatabaseStatus = "reachable"
	DatabaseUnavailable DatabaseStatus = "unavailable"
)

type Capability string

const (
	CapabilityQueryStats  Capability = "query_stats"
	CapabilityBufferCache Capability = "buffer_cache"
)

type Reason string

const (
	ReasonRegistration  Reason = "registration"
	ReasonConfigChanged Reason = "config_changed"
	ReasonStartup       Reason = "startup"
	ReasonErrors        Reason = "collector_errors"
	ReasonManual        Reason = "manual"
)

type CapabilityResult struct {
	Status           string
	ReasonCode       string
	ExtensionVersion string
	Error            string
}

type Snapshot struct {
	ID             uuid.UUID
	DatabaseID     uuid.UUID
	ConfigRevision int64
	DatabaseStatus string
	Capabilities   map[Capability]CapabilityResult
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
