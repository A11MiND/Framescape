-- +goose Up
-- Backs Community's like button: one row per (asset, user) — the unique key
-- both enforces "once per person per work" and makes like/unlike idempotent
-- (INSERT IGNORE / DELETE against a row that may not exist, no separate
-- existence check needed). Counts are computed on read (COUNT(*) grouped by
-- asset_id) rather than a denormalized counter column — this codebase's own
-- established POC-scale simplicity call (see upkeep.go's own doc on the
-- same tradeoff elsewhere), and feed/list pages here are always small,
-- limited result sets.
CREATE TABLE asset_likes (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  asset_id   BIGINT UNSIGNED NOT NULL,
  user_id    BIGINT UNSIGNED NOT NULL,
  created_at DATETIME NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_asset_user (asset_id, user_id),
  KEY idx_asset (asset_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- +goose Down
DROP TABLE asset_likes;
