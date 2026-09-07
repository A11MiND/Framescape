-- +goose Up
-- Admin dashboard's account lifecycle (§ create/deactivate a user for
-- someone the admin doesn't want to hand their own login to) needs a way
-- to block a specific account from authenticating without deleting it —
-- nothing in this schema did that before. Defaults to TRUE for every
-- existing row so this migration never locks anyone already using the
-- platform out.
ALTER TABLE users ADD COLUMN is_active BOOLEAN NOT NULL DEFAULT TRUE;

-- +goose Down
ALTER TABLE users DROP COLUMN is_active;
