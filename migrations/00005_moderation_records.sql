-- +goose Up
-- W7 (F8.4): audit trail for every content-safety rejection. §9's schema
-- listing names this table but says its layout "follows v0.1, omitted
-- here" — v0.1 isn't available in this project, so this is a fresh,
-- minimal design covering exactly what F8.4 needs: which task/job/user hit
-- a moderation rejection, and the provider's own classification for it.
-- Populated by internal/application/projection whenever a task fails with
-- the "sensitive_content:" prefix already used by minimax.image/minimax.video's
-- §10.4 error classification (image body_resp 1026, video HTTP 422).
CREATE TABLE moderation_records (
  id                  BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  task_run_id         VARCHAR(64)  NOT NULL,
  job_id              BIGINT UNSIGNED NOT NULL,
  user_id             BIGINT UNSIGNED NOT NULL,
  provider_code       VARCHAR(32)  NOT NULL DEFAULT 'minimax',
  executor_type       VARCHAR(32)  NOT NULL DEFAULT '',
  provider_message    VARCHAR(512) NOT NULL DEFAULT '',
  created_at          DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uk_task_run (task_run_id),
  KEY idx_job (job_id),
  KEY idx_user_time (user_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- +goose Down
DROP TABLE IF EXISTS moderation_records;
