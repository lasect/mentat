package prober

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mentat/internal/appdb"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ProbeLog struct {
	ID          uuid.UUID
	DatabaseIDs []uuid.UUID
	Reason      Reason
	StartedAt   time.Time
	CompletedAt *time.Time
}

// ProberDatabaseSnapshot retains the complete outcome, including partial results.
// Reason is stored on the log and restored when snapshots are read.
type ProberDatabaseSnapshot = ProberResult

var (
	ErrProbeLogNotFound      = errors.New("probe log not found")
	ErrInvalidProbeData      = errors.New("invalid probe data")
	ErrProbeSnapshotConflict = errors.New("different snapshot already saved for this database and event")
	ErrProbeLogUnfinished    = errors.New("probe log has missing outcomes or a finish time before its outcomes")
)

type ProbeStore struct {
	pool    *pgxpool.Pool
	queries *appdb.Queries
}

func NewProbeStore(pool *pgxpool.Pool) (*ProbeStore, error) {
	if pool == nil {
		return nil, fmt.Errorf("probe store requires a database pool")
	}
	return &ProbeStore{pool: pool, queries: appdb.New(pool)}, nil
}

// CreateLog records the intended targets before any database is probed.
func (s *ProbeStore) CreateLog(ctx context.Context, log ProbeLog) error {
	if log.ID == uuid.Nil || log.StartedAt.IsZero() || log.CompletedAt != nil || strings.TrimSpace(string(log.Reason)) == "" || len(log.DatabaseIDs) == 0 {
		return fmt.Errorf("%w: log requires an ID, reason, start time, targets, and no finish time", ErrInvalidProbeData)
	}
	seen := make(map[uuid.UUID]bool, len(log.DatabaseIDs))
	for _, id := range log.DatabaseIDs {
		if id == uuid.Nil || seen[id] {
			return fmt.Errorf("%w: database targets must be nonzero and unique", ErrInvalidProbeData)
		}
		seen[id] = true
	}
	if err := s.queries.CreateProbeLog(ctx, appdb.CreateProbeLogParams{
		ID: log.ID, DatabaseIds: log.DatabaseIDs, Reason: string(log.Reason), StartedAt: probeTimestamp(log.StartedAt),
	}); err != nil {
		return fmt.Errorf("create probe log: %w", err)
	}
	return nil
}

// SaveDatabaseSnapshot records a returned outcome, even if the probe failed.
// Exact retries are safe; a different outcome needs a new event ID.
func (s *ProbeStore) SaveDatabaseSnapshot(ctx context.Context, snapshot ProberDatabaseSnapshot) error {
	if err := validateProbeSnapshot(snapshot); err != nil {
		return err
	}
	extensions := snapshot.Extensions
	if extensions == nil {
		extensions = map[ExtensionName]ExtensionResult{}
	}
	encoded, err := json.Marshal(extensions)
	if err != nil {
		return fmt.Errorf("encode probe extensions: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin save probe snapshot: %w", err)
	}
	defer tx.Rollback(ctx)
	q := s.queries.WithTx(tx)
	log, err := q.LockProbeLog(ctx, snapshot.EventID)
	if err != nil {
		return probeLogError(err)
	}
	if !slices.Contains(log.DatabaseIds, snapshot.DatabaseID) || string(snapshot.Reason) != log.Reason {
		return fmt.Errorf("%w: database and reason must match the probe log", ErrInvalidProbeData)
	}
	if probeTimestamp(snapshot.StartedAt).Time.Before(log.StartedAt.Time) ||
		(log.CompletedAt.Valid && probeTimestamp(snapshot.CompletedAt).Time.After(log.CompletedAt.Time)) {
		return fmt.Errorf("%w: snapshot times must fall within the probe log", ErrInvalidProbeData)
	}
	rows, err := q.SaveProbeDatabaseSnapshot(ctx, appdb.SaveProbeDatabaseSnapshotParams{
		EventID: snapshot.EventID, DatabaseID: snapshot.DatabaseID,
		DatabaseStatus: string(snapshot.DatabaseStatus), Status: string(snapshot.Status),
		ErrorMessage: snapshot.ErrorMessage, Extensions: encoded,
		StartedAt: probeTimestamp(snapshot.StartedAt), CompletedAt: probeTimestamp(snapshot.CompletedAt),
	})
	if err != nil {
		return fmt.Errorf("save probe snapshot: %w", err)
	}
	if rows != 1 {
		return ErrProbeSnapshotConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit probe snapshot: %w", err)
	}
	return nil
}

// FinishLog requires an outcome for every target, including failed probes.
// Repeated calls preserve the original finish time. The log lock serializes
// finalization with snapshot saves so an event cannot finish before its writes.
func (s *ProbeStore) FinishLog(ctx context.Context, eventID uuid.UUID, completedAt time.Time) error {
	if eventID == uuid.Nil || completedAt.IsZero() {
		return fmt.Errorf("%w: event ID and finish time are required", ErrInvalidProbeData)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin finish probe log: %w", err)
	}
	defer tx.Rollback(ctx)
	q := s.queries.WithTx(tx)
	log, err := q.LockProbeLog(ctx, eventID)
	if err != nil {
		return probeLogError(err)
	}
	if log.CompletedAt.Valid {
		return nil
	}
	rows, err := q.FinishProbeLog(ctx, appdb.FinishProbeLogParams{
		EventID: eventID, CompletedAt: probeTimestamp(completedAt),
	})
	if err != nil {
		return fmt.Errorf("finish probe log: %w", err)
	}
	if rows != 1 {
		return ErrProbeLogUnfinished
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit finished probe log: %w", err)
	}
	return nil
}

func (s *ProbeStore) GetLog(ctx context.Context, eventID uuid.UUID) (ProbeLog, error) {
	row, err := s.queries.GetProbeLog(ctx, eventID)
	if err != nil {
		return ProbeLog{}, probeLogError(err)
	}
	log := ProbeLog{ID: row.ID, DatabaseIDs: row.DatabaseIds, Reason: Reason(row.Reason), StartedAt: row.StartedAt.Time}
	if row.CompletedAt.Valid {
		log.CompletedAt = &row.CompletedAt.Time
	}
	return log, nil
}

func (s *ProbeStore) ListDatabaseSnapshots(ctx context.Context, eventID uuid.UUID) ([]ProberDatabaseSnapshot, error) {
	rows, err := s.queries.ListProbeDatabaseSnapshots(ctx, eventID)
	if err != nil {
		return nil, fmt.Errorf("list probe snapshots: %w", err)
	}
	snapshots := make([]ProberDatabaseSnapshot, 0, len(rows))
	for _, row := range rows {
		snapshot := ProberDatabaseSnapshot{
			EventID: row.EventID, DatabaseID: row.DatabaseID, Reason: Reason(row.Reason),
			DatabaseStatus: DatabaseStatus(row.DatabaseStatus), Status: ProbeStatus(row.Status),
			ErrorMessage: row.ErrorMessage, StartedAt: row.StartedAt.Time, CompletedAt: row.CompletedAt.Time,
		}
		if err := json.Unmarshal(row.Extensions, &snapshot.Extensions); err != nil {
			return nil, fmt.Errorf("decode probe extensions for database %s: %w", row.DatabaseID, err)
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, nil
}

func probeTimestamp(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t.UTC().Truncate(time.Microsecond), Valid: true}
}

func probeLogError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrProbeLogNotFound
	}
	return fmt.Errorf("read probe log: %w", err)
}

func validateProbeSnapshot(snapshot ProberDatabaseSnapshot) error {
	if snapshot.EventID == uuid.Nil || snapshot.DatabaseID == uuid.Nil || snapshot.StartedAt.IsZero() || snapshot.CompletedAt.IsZero() || snapshot.CompletedAt.Before(snapshot.StartedAt) {
		return fmt.Errorf("%w: snapshot requires IDs and ordered start/finish times", ErrInvalidProbeData)
	}
	if snapshot.Status != ProbeCompleted && snapshot.Status != ProbeIncomplete {
		return fmt.Errorf("%w: unknown probe status", ErrInvalidProbeData)
	}
	switch snapshot.DatabaseStatus {
	case DatabaseUnknown, DatabaseReachable, DatabaseUnavailable:
	default:
		return fmt.Errorf("%w: unknown database status", ErrInvalidProbeData)
	}
	for name, extension := range snapshot.Extensions {
		if name == "" || extension.Name != name {
			return fmt.Errorf("%w: extension name must match its map key", ErrInvalidProbeData)
		}
		switch extension.Readiness {
		case ReadinessReady, ReadinessMissingDependency, ReadinessPermissionDenied, ReadinessUnsupported, ReadinessUnavailable:
		case ReadinessUnknown:
			if snapshot.Status == ProbeCompleted {
				return fmt.Errorf("%w: completed probe has unknown readiness", ErrInvalidProbeData)
			}
		default:
			return fmt.Errorf("%w: unknown extension readiness", ErrInvalidProbeData)
		}
	}
	return nil
}
