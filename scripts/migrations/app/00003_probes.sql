-- +goose Up
CREATE TABLE probe_logs (
    id uuid PRIMARY KEY,
    database_ids uuid[] NOT NULL,
    reason text NOT NULL,
    started_at timestamptz NOT NULL,
    completed_at timestamptz,
    CONSTRAINT probe_logs_targets_not_empty CHECK (cardinality(database_ids) > 0),
    CONSTRAINT probe_logs_targets_not_null CHECK (array_position(database_ids, NULL) IS NULL),
    CONSTRAINT probe_logs_reason_not_empty CHECK (btrim(reason) <> ''),
    CONSTRAINT probe_logs_time_order CHECK (completed_at >= started_at)
);

CREATE TABLE probe_database_snapshots (
    event_id uuid NOT NULL REFERENCES probe_logs(id) ON DELETE CASCADE,
    -- Preserve historical results even after a configured database is deleted.
    database_id uuid NOT NULL,
    database_status text NOT NULL,
    status text NOT NULL,
    error_message text NOT NULL DEFAULT '',
    extensions jsonb NOT NULL,
    started_at timestamptz NOT NULL,
    completed_at timestamptz NOT NULL,
    PRIMARY KEY (event_id, database_id),
    CONSTRAINT probe_snapshots_database_status CHECK (
        database_status IN ('unknown', 'reachable', 'unavailable')
    ),
    CONSTRAINT probe_snapshots_status CHECK (status IN ('completed', 'incomplete')),
    CONSTRAINT probe_snapshots_extensions_object CHECK (jsonb_typeof(extensions) = 'object'),
    CONSTRAINT probe_snapshots_time_order CHECK (completed_at >= started_at)
);

CREATE INDEX probe_snapshots_database_history_idx
    ON probe_database_snapshots (database_id, started_at DESC);

-- +goose Down
DROP TABLE IF EXISTS probe_database_snapshots;
DROP TABLE IF EXISTS probe_logs;
