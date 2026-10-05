package collection

import _ "embed"

// Embedded queries use fixed limits and require no runtime parameters.
//
//go:embed sql/pg_stat_monitor.sql
var statMonitorSQL string

//go:embed sql/pg_wait_sampling.sql
var waitSamplingSQL string

//go:embed sql/pg_stat_kcache.sql
var statKCacheSQL string

//go:embed sql/pg_qualstats.sql
var qualStatsSQL string

//go:embed sql/pgsentinel.sql
var sentinelSQL string

// NewDefaultBuilder registers bounded snapshots for the five supported extensions.
// See README.md for scope, compatibility, and snapshot truncation semantics.
func NewDefaultBuilder() (*Builder, error) {
	return NewBuilder([]QuerySpec{
		{"pg_stat_monitor", "statements", statMonitorSQL},
		{"pg_wait_sampling", "profile", waitSamplingSQL},
		{"pg_stat_kcache", "query_resources", statKCacheSQL},
		{"pg_qualstats", "predicates", qualStatsSQL},
		{"pgsentinel", "active_session_history", sentinelSQL},
	})
}
