-- +goose Up
-- Items a provider stops returning are marked removed rather than deleted: they disappear from
-- search, and come back if the provider returns them again.
ALTER TABLE contents ADD COLUMN removed_at TIMESTAMPTZ;
ALTER TABLE sync_runs ADD COLUMN removed INT NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE sync_runs DROP COLUMN removed;
ALTER TABLE contents DROP COLUMN removed_at;
