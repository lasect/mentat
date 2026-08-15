package probe

import (
	"mentat/internal/appdb"
)

type probe struct {
	queries appdb.Queries
}

func initializeProbe(db appdb.Queries) *probe {
	return &probe{
		queries: db,
	}
}
