-- +goose Up
ALTER TABLE database_extensions
ADD COLUMN last_collected_at timestamptz,
ADD COLUMN next_run_at timestamptz;

-- +goose Down
ALTER TABLE database_extensions
DROP COLUMN IF EXISTS next_run_at,
DROP COLUMN IF EXISTS last_collected_at;
