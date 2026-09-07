-- +goose Up
-- Admin dashboard (usage overview, user list, manual credit grants) needs a
-- way to distinguish operator accounts from ordinary users — nothing in
-- this schema did that before. Defaults to FALSE for every existing row;
-- the first admin has to be flipped by hand (direct SQL, same "operator
-- action, not a user-facing endpoint" reasoning as cmd/cli's grant-credits)
-- since there's no admin yet to grant it through the UI.
ALTER TABLE users ADD COLUMN is_admin BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose Down
ALTER TABLE users DROP COLUMN is_admin;
