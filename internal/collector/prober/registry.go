package prober

import (
	"context"
	"mentat/internal/clientdb"
)

type CollectorName string

type Querier clientdb.PoolManager

const (
	CollectorQueryStats     CollectorName = "query_statistics"
	CollectorActiveSessions CollectorName = "active_sessions"
	CollectorTableStats     CollectorName = "table_statistics"
)

type CollectorSpec struct {
	name                Capability
	RequiredExtensions  string
	MinExtensionVersion string
	MinimumPGVersion    int
	check               CheckFunc
}

type ProberRegistery struct {
	registery map[CollectorName]CollectorSpec
}

type CheckFunc func(
	ctx context.Context,
	db Querier,
) CollectorResult
