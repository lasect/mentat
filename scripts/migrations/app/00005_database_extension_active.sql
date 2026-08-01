-- +goose Up
ALTER TABLE database_extensions
ADD COLUMN is_active boolean NOT NULL DEFAULT true;

-- +goose Down
ALTER TABLE database_extensions
DROP COLUMN IF EXISTS is_active;
