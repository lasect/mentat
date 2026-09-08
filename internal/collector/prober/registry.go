package prober

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

type ExtensionName string

const (
	ExtPGStatMonitor  ExtensionName = "pg_stat_monitor"
	ExtPGWaitSampling ExtensionName = "pg_wait_sampling"
	ExtPGStatKCache   ExtensionName = "pg_stat_kcache"
	ExtPGQualStats    ExtensionName = "pg_qualstats"
	ExtPGSentinel     ExtensionName = "pgsentinel"
)

type Querier interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

type ExtensionSpec struct {
	MinVersion       string
	MinimumPGVersion int
	check            CheckFunc
}

type ProberRegistry struct {
	registry map[ExtensionName]ExtensionSpec
}

type CheckFunc func(
	ctx context.Context,
	db Querier,
) ExtensionResult

func NewProberRegistry() *ProberRegistry {
	return &ProberRegistry{
		registry: map[ExtensionName]ExtensionSpec{
			ExtPGStatMonitor: {
				MinVersion:       "2.0.0",
				MinimumPGVersion: 14,
				check:            checkPGStatMonitor,
			},
			ExtPGWaitSampling: {
				MinVersion:       "1.1.4",
				MinimumPGVersion: 14,
				check:            checkPGWaitSampling,
			},
			ExtPGStatKCache: {
				MinVersion:       "2.3.0",
				MinimumPGVersion: 14,
				check:            checkPGStatKCache,
			},
			ExtPGQualStats: {
				MinVersion:       "2.1.0",
				MinimumPGVersion: 14,
				check:            checkPGQualStats,
			},
			ExtPGSentinel: {
				MinVersion:       "1.4.0",
				MinimumPGVersion: 14,
				check:            checkPGSentinel,
			},
		},
	}
}

func checkPGStatMonitor(ctx context.Context, db Querier) ExtensionResult {
	return checkExtension(ctx, db, ExtPGStatMonitor, "SELECT * FROM pg_stat_monitor LIMIT 1")
}
func checkPGWaitSampling(ctx context.Context, db Querier) ExtensionResult {
	return checkExtension(ctx, db, ExtPGWaitSampling, "SELECT * FROM pg_wait_sampling_profile LIMIT 1")
}
func checkPGStatKCache(ctx context.Context, db Querier) ExtensionResult {
	return checkExtension(ctx, db, ExtPGStatKCache, "SELECT * FROM pg_stat_kcache LIMIT 1")
}
func checkPGQualStats(ctx context.Context, db Querier) ExtensionResult {
	return checkExtension(ctx, db, ExtPGQualStats, "SELECT * FROM pg_qualstats LIMIT 1")
}
func checkPGSentinel(ctx context.Context, db Querier) ExtensionResult {
	return checkExtension(ctx, db, ExtPGSentinel, "SELECT * FROM pg_active_session_history LIMIT 1")
}

func checkExtension(ctx context.Context, db Querier, name ExtensionName, query string) ExtensionResult {
	result := ExtensionResult{Name: name, Readiness: ReadinessReady}
	// Exec consumes the result without depending on extension-specific columns.
	// A successful query is ready even when the view has no samples yet.
	_, err := db.Exec(ctx, query)
	if err == nil {
		return result
	}

	result.Readiness = ReadinessUnavailable
	result.ErrorMessage = err.Error()
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "42P01", "42883": // Undefined relation or function.
			result.Readiness = ReadinessMissingDependency
		case "42501": // Insufficient privilege.
			result.Readiness = ReadinessPermissionDenied
		case "0A000": // Feature not supported.
			result.Readiness = ReadinessUnsupported
		}
	}
	return result
}
