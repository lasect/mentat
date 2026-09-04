package prober

import (
	"context"
	"mentat/internal/clientdb"
)

type ExtensionName string

const (
	ExtensionPGStatMonitor  ExtensionName = "pg_stat_monitor"
	ExtensionPGWaitSampling ExtensionName = "pg_wait_sampling"
	ExtensionPGStatKcache   ExtensionName = "pg_stat_kcache"
	ExtensionPGQualstats    ExtensionName = "pg_qualstats"
	ExtensionPGSentinel     ExtensionName = "pgsentinel"
)

type Querier clientdb.PoolManager

type ExtensionSpec struct {
	name             ExtensionName
	Name             string
	MinVersion       string
	MinimumPGVersion int
	check            CheckFunc
}

type ProberRegistery struct {
	registery map[ExtensionName]ExtensionSpec
}

type CheckFunc func(
	ctx context.Context,
	db Querier,
) CheckResult

type CheckResult struct {
}
