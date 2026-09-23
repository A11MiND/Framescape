-- +goose Up
-- Gray release for the OpenAI-backed comic editor (/comics): generation is
-- expensive, so it's opt-in per account, toggled from the admin users page.
-- Admins are always allowed (jobsvc.ComicAIAllowed) and need no row change.
ALTER TABLE users ADD COLUMN comic_ai_enabled BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose Down
ALTER TABLE users DROP COLUMN comic_ai_enabled;
