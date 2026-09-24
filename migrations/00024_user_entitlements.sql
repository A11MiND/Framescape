-- +goose Up
-- Per-user feature access (gray releases). openai_image grants OpenAI image
-- generation; it replaces users.comic_ai_enabled, whose rows move here.
CREATE TABLE user_entitlements (
  user_id     BIGINT UNSIGNED NOT NULL,
  entitlement VARCHAR(32)     NOT NULL,
  granted_by  BIGINT UNSIGNED NULL,
  created_at  DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (user_id, entitlement)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

INSERT INTO user_entitlements (user_id, entitlement)
SELECT id, 'openai_image' FROM users WHERE comic_ai_enabled = TRUE;

-- +goose Down
UPDATE users u JOIN user_entitlements e ON e.user_id = u.id AND e.entitlement = 'openai_image' SET u.comic_ai_enabled = TRUE;
DROP TABLE user_entitlements;
