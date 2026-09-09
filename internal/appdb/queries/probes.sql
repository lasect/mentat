-- name: CreateProbeLog :exec
INSERT INTO probe_logs (id, database_ids, reason, started_at)
VALUES ($1, $2, $3, $4);

-- name: GetProbeLog :one
SELECT * FROM probe_logs WHERE id = $1;

-- name: LockProbeLog :one
SELECT * FROM probe_logs WHERE id = $1 FOR UPDATE;

-- name: SaveProbeDatabaseSnapshot :execrows
INSERT INTO probe_database_snapshots (
    event_id, database_id, database_status, status, error_message,
    extensions, started_at, completed_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (event_id, database_id) DO UPDATE
SET event_id = EXCLUDED.event_id
WHERE probe_database_snapshots.database_status = EXCLUDED.database_status
  AND probe_database_snapshots.status = EXCLUDED.status
  AND probe_database_snapshots.error_message = EXCLUDED.error_message
  AND probe_database_snapshots.extensions = EXCLUDED.extensions
  AND probe_database_snapshots.started_at = EXCLUDED.started_at
  AND probe_database_snapshots.completed_at = EXCLUDED.completed_at;

-- name: FinishProbeLog :execrows
UPDATE probe_logs AS logs
SET completed_at = sqlc.arg(completed_at)
WHERE logs.id = sqlc.arg(event_id)
  AND logs.completed_at IS NULL
  AND sqlc.arg(completed_at) >= logs.started_at
  AND NOT EXISTS (
      SELECT 1 FROM unnest(logs.database_ids) AS targets(database_id)
      WHERE NOT EXISTS (
          SELECT 1 FROM probe_database_snapshots AS snapshots
          WHERE snapshots.event_id = logs.id
            AND snapshots.database_id = targets.database_id
      )
  )
  AND NOT EXISTS (
      SELECT 1 FROM probe_database_snapshots AS snapshots
      WHERE snapshots.event_id = logs.id
        AND snapshots.completed_at > sqlc.arg(completed_at)
  );

-- name: ListProbeDatabaseSnapshots :many
SELECT snapshots.*, logs.reason
FROM probe_database_snapshots AS snapshots
JOIN probe_logs AS logs ON logs.id = snapshots.event_id
WHERE snapshots.event_id = $1
ORDER BY snapshots.started_at, snapshots.database_id;
