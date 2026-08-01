-- +goose Up
ALTER TABLE database_extensions
ADD COLUMN last_interval timestamptz,
ADD COLUMN next_interval timestamptz;

-- +goose Down
ALTER TABLE database_extensions
DROP COLUMN IF EXISTS next_interval,
DROP COLUMN IF EXISTS last_interval;
