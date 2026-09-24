-- +goose Up
-- Small preview images for grids (library, tasks, community); the original
-- stays in public_url. Filled asynchronously after an asset is created.
ALTER TABLE assets ADD COLUMN thumb_url VARCHAR(512) NOT NULL DEFAULT '' AFTER thumb_key;

-- +goose Down
ALTER TABLE assets DROP COLUMN thumb_url;
