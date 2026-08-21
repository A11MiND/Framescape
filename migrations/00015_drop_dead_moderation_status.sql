-- +goose Up
-- assets.moderation_status (00001) has never been read anywhere since it
-- was added — every asset is written "pending" (asset_sink.go, assets.go's
-- handleCompleteAsset) and nothing ever advances or checks it, confirmed by
-- grep before writing this migration; migration 00010's own comment already
-- flagged this as a known dead column when it landed. The real moderation
-- pipeline is moderation_records (00005), wired into
-- internal/application/projection's maybeRecordModeration and genuinely
-- checked against executor rejections and post-hoc review — this column was
-- never that pipeline, just unused leftover surface area from before it
-- existed. Dropped rather than left in place per this codebase's own
-- "if it's unused, delete it completely" rule.
ALTER TABLE assets
  DROP COLUMN moderation_status;

-- +goose Down
ALTER TABLE assets
  ADD COLUMN moderation_status VARCHAR(16) NOT NULL DEFAULT 'pending';
