package prober

import (
	"context"
	"mentat/internal/clientdb"
)

type ExtensionName string

const (
	ExtPGStatMonitor  ExtensionName = "pg_stat_monitor"
	ExtPGWaitSampling ExtensionName = "pg_wait_sampling"
	ExtPGStatKCache   ExtensionName = "pg_stat_kcache"
	ExtPGQualStats    ExtensionName = "pg_qualstats"
	ExtPGSentinel     ExtensionName = "pgsentinel"
)

type Querier clientdb.PoolManager

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
	return ExtensionResult{
		Name:         ExtPGStatMonitor,
		Readiness:    ,
		ErrorMessage: "",
	}
}
func checkPGWaitSampling(ctx context.Context, db Querier) ExtensionResult {
	return ExtensionResult{
		Name:         ExtPGWaitSampling,
		Readiness:    ,
		ErrorMessage: "",
	}
}
func checkPGStatKCache(ctx context.Context, db Querier) ExtensionResult {
	return ExtensionResult{
		Name:         ExtPGStatKCache,
		Readiness:    ,
		ErrorMessage: "",
	}
}
func checkPGQualStats(ctx context.Context, db Querier) ExtensionResult {
	return ExtensionResult{
		Name:         ExtPGQualStats,
		Readiness:    ,
		ErrorMessage: "",
	}
}
func checkPGSentinel(ctx context.Context, db Querier) ExtensionResult {
	return ExtensionResult{
		Name:         ExtPGSentinel,
		Readiness:    ,
		ErrorMessage: "",
	}
}
