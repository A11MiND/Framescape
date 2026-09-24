-- +goose Up
-- Orchestrator v2: job_nodes becomes the source of truth for execution state
-- (it was a projection of the Aether store before). Every state transition is
-- a compare-and-set on these rows, so any api/worker process can advance any
-- job and no single scheduler process is needed. Rows written by the old
-- engine keep only their `phase` column filled and are read-only history
-- (jobs.engine = 'aether').

ALTER TABLE jobs
  ADD COLUMN engine         VARCHAR(16)  NOT NULL DEFAULT 'aether' AFTER workflow_run_id,
  ADD COLUMN plan_meta      JSON         NULL,
  ADD COLUMN version        INT UNSIGNED NOT NULL DEFAULT 0,
  ADD COLUMN deadline_at    DATETIME(3)  NULL,
  ADD COLUMN cover_asset_id VARCHAR(32)  NOT NULL DEFAULT '',
  ADD KEY idx_user_list (user_id, deleted_at, id),
  ADD KEY idx_user_status_list (user_id, status, deleted_at, id),
  ADD KEY idx_project (project_id),
  ADD KEY idx_status_deadline (status, deadline_at);

ALTER TABLE job_nodes
  ADD COLUMN status           VARCHAR(16)   NOT NULL DEFAULT '',
  ADD COLUMN queue            VARCHAR(24)   NOT NULL DEFAULT '',
  ADD COLUMN deps_pending     INT           NOT NULL DEFAULT 0,
  ADD COLUMN deps             JSON          NULL,
  ADD COLUMN inputs_json      JSON          NULL,
  ADD COLUMN outputs_json     JSON          NULL,
  ADD COLUMN check_rule       VARCHAR(32)   NOT NULL DEFAULT '',
  ADD COLUMN attempt          INT           NOT NULL DEFAULT 0,
  ADD COLUMN max_attempts     INT           NOT NULL DEFAULT 1,
  ADD COLUMN timeout_ms       INT           NOT NULL DEFAULT 0,
  ADD COLUMN dispatch_seq     INT           NOT NULL DEFAULT 0,
  ADD COLUMN dispatched_at    DATETIME(3)   NULL,
  ADD COLUMN next_run_at      DATETIME(3)   NULL,
  ADD COLUMN queue_reason     VARCHAR(32)   NOT NULL DEFAULT '',
  ADD COLUMN lease_owner      VARCHAR(64)   NOT NULL DEFAULT '',
  ADD COLUMN lease_until      DATETIME(3)   NULL,
  ADD COLUMN cancel_requested TINYINT(1)    NOT NULL DEFAULT 0,
  ADD COLUMN provider_ref     JSON          NULL,
  ADD COLUMN provider_task_id VARCHAR(128)  NOT NULL DEFAULT '',
  ADD COLUMN reserved_credits INT           NOT NULL DEFAULT 0,
  ADD COLUMN cost_yuan        DECIMAL(14,6) NOT NULL DEFAULT 0,
  ADD COLUMN error_code       VARCHAR(64)   NOT NULL DEFAULT '',
  ADD COLUMN display          JSON          NULL,
  ADD COLUMN version          INT UNSIGNED  NOT NULL DEFAULT 0,
  ADD COLUMN created_at       DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  ADD KEY idx_job_name (job_id, node_name),
  ADD KEY idx_status_next (status, next_run_at),
  ADD KEY idx_status_lease (status, lease_until),
  ADD KEY idx_provider_task (provider_task_id);

-- Event outbox and per-user timeline: written in the same transaction as the
-- state change it describes, then published to Redis. SSE clients resume with
-- Last-Event-ID from here, so a missed publish only delays delivery.
CREATE TABLE job_events (
  id           BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id      BIGINT UNSIGNED NOT NULL,
  job_id       BIGINT UNSIGNED NOT NULL,
  job_biz_id   CHAR(26)        NOT NULL,
  type         VARCHAR(32)     NOT NULL,
  payload      JSON            NOT NULL,
  published_at DATETIME(3)     NULL,
  created_at   DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  KEY idx_user_id (user_id, id),
  KEY idx_unpublished (published_at, id),
  KEY idx_job (job_id, id),
  KEY idx_created (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- One row per external provider call, written before the request goes out.
-- It is the source for real-currency spend reporting and lets a crashed
-- worker's in-flight paid call be found instead of silently repeated.
CREATE TABLE provider_calls (
  id               BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  provider         VARCHAR(16)     NOT NULL,
  model            VARCHAR(64)     NOT NULL DEFAULT '',
  operation        VARCHAR(48)     NOT NULL,
  user_id          BIGINT UNSIGNED NULL,
  job_id           BIGINT UNSIGNED NULL,
  node_id          BIGINT UNSIGNED NULL,
  attempt          INT             NOT NULL DEFAULT 0,
  idem_key         VARCHAR(128)    NULL,
  provider_task_id VARCHAR(128)    NOT NULL DEFAULT '',
  status           VARCHAR(16)     NOT NULL,
  http_status      INT             NOT NULL DEFAULT 0,
  error_code       VARCHAR(64)     NOT NULL DEFAULT '',
  latency_ms       INT             NOT NULL DEFAULT 0,
  usage_json       JSON            NULL,
  cost_yuan        DECIMAL(14,6)   NOT NULL DEFAULT 0,
  started_at       DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  finished_at      DATETIME(3)     NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_idem (idem_key),
  KEY idx_job (job_id),
  KEY idx_started (started_at),
  KEY idx_status_started (status, started_at),
  KEY idx_provider_task (provider, provider_task_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Per-job reservation. credit_accounts.held is the sum of open holds'
-- remaining, so settling one job can never consume another job's reservation.
CREATE TABLE credit_holds (
  id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id     BIGINT UNSIGNED NOT NULL,
  job_id      BIGINT UNSIGNED NOT NULL,
  job_biz_id  CHAR(26)        NOT NULL,
  held_total  INT             NOT NULL DEFAULT 0,
  committed   INT             NOT NULL DEFAULT 0,
  overage     INT             NOT NULL DEFAULT 0,
  released    INT             NOT NULL DEFAULT 0,
  remaining   INT             NOT NULL DEFAULT 0,
  status      VARCHAR(16)     NOT NULL DEFAULT 'open',
  created_at  DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at  DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uk_job (job_id),
  KEY idx_user_status (user_id, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- +goose Down
DROP TABLE credit_holds;
DROP TABLE provider_calls;
DROP TABLE job_events;

ALTER TABLE job_nodes
  DROP KEY idx_provider_task,
  DROP KEY idx_status_lease,
  DROP KEY idx_status_next,
  DROP KEY idx_job_name,
  DROP COLUMN created_at,
  DROP COLUMN version,
  DROP COLUMN display,
  DROP COLUMN error_code,
  DROP COLUMN cost_yuan,
  DROP COLUMN reserved_credits,
  DROP COLUMN provider_task_id,
  DROP COLUMN provider_ref,
  DROP COLUMN cancel_requested,
  DROP COLUMN lease_until,
  DROP COLUMN lease_owner,
  DROP COLUMN queue_reason,
  DROP COLUMN next_run_at,
  DROP COLUMN dispatched_at,
  DROP COLUMN dispatch_seq,
  DROP COLUMN timeout_ms,
  DROP COLUMN max_attempts,
  DROP COLUMN attempt,
  DROP COLUMN check_rule,
  DROP COLUMN outputs_json,
  DROP COLUMN inputs_json,
  DROP COLUMN deps,
  DROP COLUMN deps_pending,
  DROP COLUMN queue,
  DROP COLUMN status;

ALTER TABLE jobs
  DROP KEY idx_status_deadline,
  DROP KEY idx_project,
  DROP KEY idx_user_status_list,
  DROP KEY idx_user_list,
  DROP COLUMN cover_asset_id,
  DROP COLUMN deadline_at,
  DROP COLUMN version,
  DROP COLUMN plan_meta,
  DROP COLUMN engine;
