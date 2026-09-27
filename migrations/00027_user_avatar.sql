-- +goose Up
ALTER TABLE users ADD COLUMN avatar_asset_id CHAR(26) NULL;

-- +goose Down
ALTER TABLE users DROP COLUMN avatar_asset_id;
