package prober

import (
	"context"
	"fmt"
	"mentat/internal/clientdb"
	"slices"
	"time"

	"github.com/google/uuid"
)

type DatabaseStatus string

const (
	DatabaseUnknown     DatabaseStatus = "unknown"
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
	registry    *ProberRegistry
	getDatabase func(uuid.UUID) (probeDatabaseClient, error)
	store       probeStore
}

type probeStore interface {
	CreateLog(context.Context, ProbeLog) error
	SaveDatabaseSnapshot(context.Context, ProberDatabaseSnapshot) error
	FinishLog(context.Context, uuid.UUID, time.Time) error
}

type probeDatabaseClient interface {
	Querier
	Ping(context.Context) error
}

type ProbeStatus string

const (
	ProbeCompleted  ProbeStatus = "completed"
	ProbeIncomplete ProbeStatus = "incomplete"
)

type Request struct {
	// EventID is supplied by the coordinator to group database results in one event.
	EventID        uuid.UUID
	DatabaseID     uuid.UUID
	ExtensionNames []ExtensionName
	Reason         Reason
	Priority       int
}

type ExtensionResult struct {
	Name         ExtensionName `json:"name"`
	Readiness    Readiness     `json:"readiness"`
	ErrorMessage string        `json:"error_message"`
	// Checked distinguishes an executed check from an unavailable or unsupported
	// result produced without executing a check.
	Checked bool `json:"checked"`
}

type ProberResult struct {
	EventID        uuid.UUID
	DatabaseID     uuid.UUID
	Reason         Reason
	DatabaseStatus DatabaseStatus
	Status         ProbeStatus
	ErrorMessage   string
	Extensions     map[ExtensionName]ExtensionResult
	StartedAt      time.Time
	CompletedAt    time.Time
}

func initializeProbe(registry *ProberRegistry, pool *clientdb.PoolManager, stores ...probeStore) *prober {
	p := &prober{
		registry: registry,
		getDatabase: func(id uuid.UUID) (probeDatabaseClient, error) {
			return pool.Get(id)
		},
	}
	if len(stores) > 0 {
		p.store = stores[0]
	}
	return p
}

// initializeProbeWithStore creates the production prober with persistence.
func initializeProbeWithStore(registry *ProberRegistry, pool *clientdb.PoolManager, store probeStore) *prober {
	return initializeProbe(registry, pool, store)
}

// Probe runs all requests as one persisted probe event. Every database result
// is saved, including incomplete results, before the method returns.
func (p *prober) Probe(ctx context.Context, requests []Request) ([]ProberResult, error) {
	if p.store == nil {
		return nil, fmt.Errorf("probe store is not configured")
	}
	if len(requests) == 0 {
		return nil, fmt.Errorf("at least one probe request is required")
	}
	eventID := requests[0].EventID
	if eventID == uuid.Nil {
		eventID = uuid.New()
	}
	startedAt := time.Now()
	targets := make([]uuid.UUID, 0, len(requests))
	seen := make(map[uuid.UUID]struct{}, len(requests))
	for _, req := range requests {
		if req.DatabaseID == uuid.Nil {
			return nil, fmt.Errorf("probe request has an empty database ID")
		}
		if req.Reason != requests[0].Reason {
			return nil, fmt.Errorf("probe requests in one event must have the same reason")
		}
		if req.EventID != uuid.Nil && req.EventID != eventID {
			return nil, fmt.Errorf("probe requests in one event must have the same event ID")
		}
		if _, exists := seen[req.DatabaseID]; exists {
			return nil, fmt.Errorf("database %s appears more than once in probe requests", req.DatabaseID)
		}
		seen[req.DatabaseID] = struct{}{}
		targets = append(targets, req.DatabaseID)
	}
	if err := p.store.CreateLog(ctx, ProbeLog{ID: eventID, DatabaseIDs: targets, Reason: requests[0].Reason, StartedAt: startedAt}); err != nil {
		return nil, fmt.Errorf("create probe log: %w", err)
	}

	results := make([]ProberResult, 0, len(requests))
	var probeErr error
	for _, req := range requests {
		req.EventID = eventID
		result, err := p.probeDatabase(ctx, req)
		results = append(results, result)
		if saveErr := p.store.SaveDatabaseSnapshot(ctx, result); saveErr != nil {
			return results, fmt.Errorf("save database %s probe snapshot: %w", req.DatabaseID, saveErr)
		}
		if err != nil && probeErr == nil {
			probeErr = err
		}
	}
	if err := p.store.FinishLog(ctx, eventID, time.Now()); err != nil {
		return results, fmt.Errorf("finish probe log: %w", err)
	}
	return results, probeErr
}

// probeDatabase returns a persistable outcome even when execution fails. Missing
// extensions and other readiness findings are results, not probe-level errors.
// The caller owns deadlines, event creation, and persistence.
func (p *prober) probeDatabase(ctx context.Context, req Request) (result ProberResult, err error) {
	result = ProberResult{
		EventID: req.EventID, DatabaseID: req.DatabaseID, Reason: req.Reason,
		DatabaseStatus: DatabaseUnknown, Status: ProbeIncomplete,
		Extensions: make(map[ExtensionName]ExtensionResult), StartedAt: time.Now(),
	}
	defer func() {
		result.CompletedAt = time.Now()
		if err != nil {
			result.ErrorMessage = err.Error()
			for name, extension := range result.Extensions {
				if !extension.Checked && extension.Readiness == ReadinessUnknown {
					extension.Readiness = ReadinessUnavailable
					extension.ErrorMessage = err.Error()
					result.Extensions[name] = extension
				}
			}
		}
	}()

	names := slices.Clone(req.ExtensionNames)
	if len(names) == 0 {
		for name := range p.registry.registry {
			names = append(names, name)
		}
		slices.Sort(names)
	}
	selected := make([]ExtensionName, 0, len(names))
	for _, name := range names {
		if _, exists := result.Extensions[name]; exists {
			continue
		}
		extension := ExtensionResult{Name: name, Readiness: ReadinessUnknown}
		if spec, exists := p.registry.registry[name]; !exists || spec.check == nil {
			extension.Readiness = ReadinessUnsupported
			extension.ErrorMessage = fmt.Sprintf("no probe registered for extension %q", name)
		} else {
			selected = append(selected, name)
		}
		result.Extensions[name] = extension
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	db, getErr := p.getDatabase(req.DatabaseID)
	if getErr != nil {
		return result, fmt.Errorf("get database pool: %w", getErr)
	}
	if pingErr := db.Ping(ctx); pingErr != nil {
		result.DatabaseStatus = DatabaseUnavailable
		return result, fmt.Errorf("ping database: %w", pingErr)
	}
	result.DatabaseStatus = DatabaseReachable
	for _, name := range selected {
		if err = ctx.Err(); err != nil {
			return result, err
		}
		extension := p.registry.registry[name].check(ctx, db)
		extension.Name = name
		extension.Checked = true
		result.Extensions[name] = extension
		if err = ctx.Err(); err != nil {
			return result, err
		}
		// An unclassified check error may be a lost connection. Verify before
		// attempting more checks; a query-specific failure can still be isolated.
		if extension.Readiness == ReadinessUnavailable {
			if pingErr := db.Ping(ctx); pingErr != nil {
				result.DatabaseStatus = DatabaseUnavailable
				return result, fmt.Errorf("database connectivity after checking %s: %w", name, pingErr)
			}
		}
	}
	// Validate coverage by name; duplicates in the request share one result.
	for _, name := range names {
		extension, exists := result.Extensions[name]
		if !exists {
			return result, fmt.Errorf("missing probe result for extension %q", name)
		}
		switch extension.Readiness {
		case ReadinessReady, ReadinessMissingDependency, ReadinessPermissionDenied,
			ReadinessUnsupported, ReadinessUnavailable:
			// These are resolved findings, including negative readiness outcomes.
		default:
			return result, fmt.Errorf("unresolved probe result for extension %q: readiness %q", name, extension.Readiness)
		}
	}
	result.Status = ProbeCompleted
	return result, nil
}
