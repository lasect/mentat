package scheduler

import (
	"crypto/cipher"
	"mentat/internal/appdb"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type extensionGroup struct {
	Key        groupKey
	Extensions []string
	NextRunAt  time.Time
}

type groupKey struct {
	DatabaseID      uuid.UUID
	IntervalSeconds int32
}

type Scheduler struct {
	pool    *pgxpool.Pool
	queries *appdb.Queries
	cipher  *ConnectionCipher
}
type ConnectionCipher struct {
	aead       cipher.AEAD
	keyVersion int16
}

func groupExtensions(rows []appdb.ListActiveExtensionsForCollectorRow) []extensionGroup {
	startupTime := time.Now()
	groups := make([]extensionGroup, 0)
	groupIndexes := make(map[groupKey]int)

	for _, row := range rows {
		key := groupKey{
			DatabaseID:      row.DatabaseID,
			IntervalSeconds: row.IntervalSeconds,
		}

		nextRunAt := startupTime
		if row.NextRunAt.Valid {
			nextRunAt = row.NextRunAt.Time
		}

		if index, exists := groupIndexes[key]; exists {
			groups[index].Extensions = append(groups[index].Extensions, row.Extension)
			if nextRunAt.Before(groups[index].NextRunAt) {
				groups[index].NextRunAt = nextRunAt
			}
			continue
		}

		groupIndexes[key] = len(groups)
		groups = append(groups, extensionGroup{
			Key:        key,
			Extensions: []string{row.Extension},
			NextRunAt:  nextRunAt,
		})
	}

	return groups
}
