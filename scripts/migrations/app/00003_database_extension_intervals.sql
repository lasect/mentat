-- +goose Up
ALTER TABLE database_extensions
ADD COLUMN interval_seconds integer NOT NULL DEFAULT 60;

ALTER TABLE database_extensions
ADD CONSTRAINT database_extensions_interval_range
CHECK (interval_seconds BETWEEN 5 AND 86400);

-- +goose Down
ALTER TABLE database_extensions
DROP CONSTRAINT IF EXISTS database_extensions_interval_range;

ALTER TABLE database_extensions
DROP COLUMN IF EXISTS interval_seconds;
