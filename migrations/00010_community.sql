-- +goose Up
-- §07's "社區功能：看別人做的作品，也可以發佈出去" ask — an asset was
-- always strictly user_id-scoped everywhere (handleGetAsset/handleListAssets/
-- handleDeleteAsset's own doc), on purpose, and stays that way; this adds an
-- explicit opt-in publish flag rather than loosening any existing ownership
-- check, so every other asset endpoint's authorization is untouched.
--
-- Known gap, called out rather than silently built around: moderation_status
-- (00001) has never actually been read anywhere — every asset is written
-- "pending" and nothing ever advances or checks it (confirmed by grep before
-- writing this migration). The community feed below does NOT filter on it,
-- since doing so would make the feed permanently empty rather than actually
-- moderated. A real deployment needs a real moderation pass wired in before
-- this ships to the public internet; this POC-scale version doesn't have one.
ALTER TABLE assets
  ADD COLUMN is_public   BOOLEAN     NOT NULL DEFAULT FALSE AFTER moderation_status,
  ADD COLUMN published_at DATETIME(3) NULL AFTER is_public,
  ADD KEY idx_public (is_public, published_at, id);

-- +goose Down
ALTER TABLE assets
  DROP KEY idx_public,
  DROP COLUMN published_at,
  DROP COLUMN is_public;
