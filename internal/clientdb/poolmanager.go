package clientdb

import (
	"context"
	"errors"
	"sync"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PoolManager struct {
	mu    sync.RWMutex
	pools map[uuid.UUID]*pgxpool.Pool
}

func NewPoolManager() *PoolManager {
	return &PoolManager{
		pools: make(map[uuid.UUID]*pgxpool.Pool),
	}
}

func (m *PoolManager) Add(ctx context.Context, databaseID uuid.UUID, connectionString string) error {
	pool, err := pgxpool.New(ctx, connectionString)
	if err != nil {
		return err
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.pools[databaseID]; exists {
		pool.Close()
		return errors.New("pool already exists")
	}

	m.pools[databaseID] = pool

	return nil
}

func (m *PoolManager) Get(databaseID uuid.UUID) (*pgxpool.Pool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	pool, ok := m.pools[databaseID]
	if !ok {
		return nil, errors.New("Can't find the pool")
	}
	return pool, nil
}

func (m *PoolManager) Remove(databaseID uuid.UUID) error {
	m.mu.Lock()

	pool, ok := m.pools[databaseID]
	if ok {
		delete(m.pools, databaseID)
	}

	m.mu.Unlock()

	if ok {
		pool.Close()
	}
	return nil
}
