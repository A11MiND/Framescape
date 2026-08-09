-- +goose Up
-- W1 (DEV_PLAN.md §5): users / credit_accounts / assets / jobs / job_nodes.
-- DDL for jobs/job_nodes/assets/credit_accounts is copied verbatim from PRD §9.2;
-- users is our own design (not specified in the PRD, which only lists F1.1's
-- requirement, not a schema).

CREATE TABLE users (
  id              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  biz_id          CHAR(26)        NOT NULL,
  email           VARCHAR(255)    NOT NULL,
  password_hash   VARCHAR(255)    NOT NULL,
  created_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uk_biz (biz_id),
  UNIQUE KEY uk_email (email)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE credit_accounts (
  user_id BIGINT UNSIGNED NOT NULL,
  balance INT NOT NULL DEFAULT 0,
  held    INT NOT NULL DEFAULT 0,
  version INT NOT NULL DEFAULT 0,
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE assets (
  id                    BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  biz_id                CHAR(26)        NOT NULL,
  user_id               BIGINT UNSIGNED NOT NULL,
  project_id            BIGINT UNSIGNED NULL,
  type                  VARCHAR(16)  NOT NULL,   -- image/video/audio
  source                VARCHAR(16)  NOT NULL,   -- upload/generated/derived
  from_task_run_id      VARCHAR(64)  NOT NULL DEFAULT '',
  parent_asset_id       BIGINT UNSIGNED NULL,

  storage_key           VARCHAR(512)  NOT NULL,
  public_url            VARCHAR(1024) NOT NULL DEFAULT '',
  thumb_key             VARCHAR(512)  NOT NULL DEFAULT '',
  mime                  VARCHAR(64)   NOT NULL,
  width                 INT NOT NULL DEFAULT 0,
  height                INT NOT NULL DEFAULT 0,
  duration_ms           INT NOT NULL DEFAULT 0,
  size_bytes            BIGINT NOT NULL DEFAULT 0,
  resolution_tag        VARCHAR(16) NOT NULL DEFAULT '',  -- 768P / 2K

  first_frame_asset_id  BIGINT UNSIGNED NULL,
  last_frame_asset_id   BIGINT UNSIGNED NULL,

  meta                  JSON NULL,   -- {seed, model, prompt_snapshot, minimax_task_id, usage}
  moderation_status     VARCHAR(16) NOT NULL DEFAULT 'pending',
  deleted_at            DATETIME(3) NULL,
  created_at            DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uk_biz (biz_id),
  KEY idx_user_type (user_id, type, deleted_at, id),
  KEY idx_project (project_id, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE jobs (
  id              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  biz_id          CHAR(26)        NOT NULL,          -- ULID，对外暴露
  user_id         BIGINT UNSIGNED NOT NULL,
  project_id      BIGINT UNSIGNED NULL,
  workflow_name   VARCHAR(64)     NOT NULL,          -- image.single / video.sequence 等（业务层命名，不受 Aether DNS-1123 约束）
  workflow_run_id VARCHAR(64)     NOT NULL DEFAULT '', -- Aether 的 RunID
  title           VARCHAR(128)    NOT NULL DEFAULT '',
  status          VARCHAR(16)     NOT NULL,          -- created/running/suspended/succeeded/partial/failed/cancelled
  spec            JSON            NOT NULL,          -- PromptSpec 快照（不可变）

  node_total      INT NOT NULL DEFAULT 0,
  node_done       INT NOT NULL DEFAULT 0,
  node_failed     INT NOT NULL DEFAULT 0,

  credit_estimated INT NOT NULL DEFAULT 0,
  credit_held      INT NOT NULL DEFAULT 0,
  credit_settled   INT NOT NULL DEFAULT 0,

  idem_key        VARCHAR(64)  NULL,
  error_code      VARCHAR(64)  NOT NULL DEFAULT '',
  error_msg       VARCHAR(512) NOT NULL DEFAULT '',
  started_at      DATETIME(3) NULL,
  finished_at     DATETIME(3) NULL,
  created_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uk_biz (biz_id),
  UNIQUE KEY uk_user_idem (user_id, idem_key),
  KEY idx_run (workflow_run_id),
  KEY idx_user_created (user_id, created_at),
  KEY idx_status (status, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 投影表：只读视图，真相在 Aether Store（PRD §9.1）
CREATE TABLE job_nodes (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  job_id        BIGINT UNSIGNED NOT NULL,
  task_run_id   VARCHAR(64)  NOT NULL,   -- Aether 的 TaskRunID
  node_name     VARCHAR(64)  NOT NULL,   -- gen-one-shot
  loop_index    INT          NOT NULL DEFAULT -1,  -- 循环轮次，非循环为 -1
  parent_scope  VARCHAR(64)  NOT NULL DEFAULT '',  -- 嵌套 scope 路径
  executor_type VARCHAR(32)  NOT NULL,
  phase         VARCHAR(16)  NOT NULL,   -- Created/Ready/Running/Succeeded/Failed/Error/Timeout/Skipped/Cancelled/Suspended
  exec_code     TINYINT      NULL,
  asset_ids     JSON         NULL,
  credit_cost   INT          NOT NULL DEFAULT 0,
  error_msg     VARCHAR(512) NOT NULL DEFAULT '',
  started_at    DATETIME(3) NULL,
  finished_at   DATETIME(3) NULL,
  updated_at    DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uk_task_run (task_run_id),
  KEY idx_job (job_id, loop_index)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- +goose Down
DROP TABLE IF EXISTS job_nodes;
DROP TABLE IF EXISTS jobs;
DROP TABLE IF EXISTS assets;
DROP TABLE IF EXISTS credit_accounts;
DROP TABLE IF EXISTS users;
