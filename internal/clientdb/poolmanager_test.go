package clientdb

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPoolManagerGet(t *testing.T) {
	t.Parallel()

	manager := NewPoolManager()
	databaseID := uuid.New()
	pool := newDisconnectedPool(t)
	manager.pools[databaseID] = pool

	got, err := manager.Get(databaseID)
	if err != nil {
		t.Fatalf("get pool: %v", err)
	}
	if got != pool {
		t.Fatalf("Get() pool = %p, want %p", got, pool)
	}
}

func TestPoolManagerGetUnknownDatabase(t *testing.T) {
	t.Parallel()

	pool, err := NewPoolManager().Get(uuid.New())
	if err == nil {
		t.Fatal("Get() error = nil, want an error")
	}
	if pool != nil {
		t.Fatalf("Get() pool = %p, want nil", pool)
	}
}

func TestPoolManagerGetOrAddReturnsExistingPool(t *testing.T) {
	t.Parallel()

	manager := NewPoolManager()
	databaseID := uuid.New()
	want := newDisconnectedPool(t)
	manager.pools[databaseID] = want

	got, err := manager.GetOrAdd(context.Background(), databaseID, "://invalid")
	if err != nil {
		t.Fatalf("GetOrAdd() existing pool error = %v, want nil", err)
	}
	if got != want {
		t.Fatalf("GetOrAdd() existing pool = %p, want %p", got, want)
	}
}

func TestPoolManagerGetOrAddDoesNotStorePoolWhenAddFails(t *testing.T) {
	t.Parallel()

	manager := NewPoolManager()
	databaseID := uuid.New()

	pool, err := manager.GetOrAdd(context.Background(), databaseID, "://invalid")
	if err == nil {
		t.Fatal("GetOrAdd() error = nil, want an invalid connection string error")
	}
	if pool != nil {
		t.Fatalf("GetOrAdd() pool = %p, want nil", pool)
	}
	if cached, getErr := manager.Get(databaseID); getErr == nil || cached != nil {
		t.Fatalf("Get() after failed GetOrAdd() = %p, %v; want nil pool and an error", cached, getErr)
	}
}

func TestPoolManagerGetReleasesReadLock(t *testing.T) {
	t.Parallel()

	manager := NewPoolManager()
	databaseID := uuid.New()
	manager.pools[databaseID] = newDisconnectedPool(t)

	const goroutineCount = 20
	var workers sync.WaitGroup
	workers.Add(goroutineCount)
	done := make(chan struct{})

	for range goroutineCount {
		go func() {
			defer workers.Done()
			for range 20 {
				pool, err := manager.Get(databaseID)
				if err != nil || pool == nil {
					t.Errorf("Get() = %p, %v; want a pool and no error", pool, err)
					return
				}
			}
		}()
	}

	go func() {
		workers.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("concurrent Get calls blocked; the manager lock was not released")
	}
}

func TestPoolManagerAddRejectsInvalidConnectionString(t *testing.T) {
	t.Parallel()

	manager := NewPoolManager()
	databaseID := uuid.New()

	if err := manager.Add(context.Background(), databaseID, "://invalid"); err == nil {
		t.Fatal("Add() error = nil, want an invalid connection string error")
	}
	if pool, err := manager.Get(databaseID); err == nil || pool != nil {
		t.Fatalf("Get() after failed Add() = %p, %v; want nil pool and an error", pool, err)
	}
}

func TestPoolManagerAddDoesNotStorePoolWhenPingFails(t *testing.T) {
	t.Parallel()

	manager := NewPoolManager()
	databaseID := uuid.New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := manager.Add(ctx, databaseID, "postgres://user:password@127.0.0.1:1/database")
	if err == nil {
		t.Fatal("Add() error = nil, want a canceled context error")
	}
	if pool, getErr := manager.Get(databaseID); getErr == nil || pool != nil {
		t.Fatalf("Get() after failed Ping() = %p, %v; want nil pool and an error", pool, getErr)
	}
}

func TestPoolManagerRemove(t *testing.T) {
	t.Parallel()

	manager := NewPoolManager()
	databaseID := uuid.New()
	pool := newDisconnectedPool(t)
	manager.pools[databaseID] = pool

	if err := manager.Remove(databaseID); err != nil {
		t.Fatalf("remove pool: %v", err)
	}
	if got, err := manager.Get(databaseID); err == nil || got != nil {
		t.Fatalf("Get() after Remove() = %p, %v; want nil pool and an error", got, err)
	}
	if err := pool.Ping(context.Background()); err == nil {
		t.Fatal("removed pool is still open")
	}
	if err := manager.Remove(databaseID); err != nil {
		t.Fatalf("remove missing pool: %v", err)
	}
}

func TestPoolManagerDatabaseLifecycle(t *testing.T) {
	databaseURL := os.Getenv("MENTAT_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("MENTAT_TEST_DATABASE_URL is not set")
	}

	manager := NewPoolManager()
	databaseID := uuid.New()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := manager.Add(ctx, databaseID, databaseURL); err != nil {
		t.Fatalf("add pool: %v", err)
	}
	t.Cleanup(func() {
		_ = manager.Remove(databaseID)
	})

	if pool, err := manager.Get(databaseID); err != nil || pool == nil {
		t.Fatalf("Get() after Add() = %p, %v; want a pool and no error", pool, err)
	}
	if pool, err := manager.GetOrAdd(ctx, databaseID, "://invalid"); err != nil || pool == nil {
		t.Fatalf("GetOrAdd() existing pool = %p, %v; want a pool and no error", pool, err)
	}
	if err := manager.Add(ctx, databaseID, databaseURL); err == nil {
		t.Fatal("duplicate Add() error = nil, want an error")
	}
	if err := manager.Remove(databaseID); err != nil {
		t.Fatalf("remove pool: %v", err)
	}
	if pool, err := manager.Get(databaseID); err == nil || pool != nil {
		t.Fatalf("Get() after Remove() = %p, %v; want nil pool and an error", pool, err)
	}

	pool, err := manager.GetOrAdd(ctx, databaseID, databaseURL)
	if err != nil {
		t.Fatalf("GetOrAdd() missing pool error = %v, want nil", err)
	}
	if pool == nil {
		t.Fatal("GetOrAdd() missing pool = nil, want a pool")
	}
}

func newDisconnectedPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	pool, err := pgxpool.New(
		context.Background(),
		"postgres://user:password@127.0.0.1:1/database",
	)
	if err != nil {
		t.Fatalf("create disconnected test pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}
