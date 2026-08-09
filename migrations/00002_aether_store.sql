-- +goose Up
-- W2 (DEV_PLAN.md §6): MySQL-backed store.Store. Column layout mirrors
-- store.WorkflowRun / store.TaskRun exactly (docs/aether-validation-report.md
-- §two.3, read from the vendored source, not guessed). PRD §9.1: "aether_*
-- 表结构必须对照仓库 store/ 目录的接口定义来设计".
--
-- run_id/workflow_run_id/parent_run_id are VARCHAR, not AUTO_INCREMENT: IDs
-- are minted by our ULIDGenerator (internal/infra/workflow/aether/idgen.go)
-- before the row exists, same ID space as every other biz_id in this system.

CREATE TABLE aether_workflow_runs (
  run_id           VARCHAR(64)   NOT NULL,
  workflow_json    JSON          NOT NULL,          -- immutable raw workflow document
  cron_workflow_id VARCHAR(64)   NOT NULL DEFAULT '', -- immutable; POC does not use CronWorkflow
  status           VARCHAR(16)   NOT NULL DEFAULT '',
  message          VARCHAR(1024) NOT NULL DEFAULT '',
  outputs_json     JSON          NULL,
  metrics_json     JSON          NULL,
  deadline         DATETIME(3)   NULL,
  token            BIGINT UNSIGNED NOT NULL DEFAULT 0,  -- optimistic lock version
  created_at       DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at       DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (run_id),
  KEY idx_status_deadline (status, deadline),
  KEY idx_cron (cron_workflow_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE aether_task_runs (
  run_id          VARCHAR(64)   NOT NULL,
  workflow_run_id VARCHAR(64)   NOT NULL,          -- immutable
  parent_run_id   VARCHAR(64)   NOT NULL DEFAULT '', -- immutable; "" = top-level scope
  depth           INT           NOT NULL DEFAULT 0,  -- immutable
  scope           VARCHAR(255)  NOT NULL DEFAULT '', -- immutable
  task_name       VARCHAR(64)   NOT NULL,             -- immutable
  template_name   VARCHAR(64)   NOT NULL,             -- immutable
  template_type   VARCHAR(16)   NOT NULL,             -- immutable: dag/task/loop
  inputs_json     JSON          NULL,
  status          VARCHAR(16)   NOT NULL DEFAULT '',
  message         VARCHAR(1024) NOT NULL DEFAULT '',
  outputs_json    JSON          NULL,
  metrics_json    JSON          NULL,
  retry_count     INT           NULL,
  deadline        DATETIME(3)   NULL,
  token           BIGINT UNSIGNED NOT NULL DEFAULT 0,  -- optimistic lock version
  created_at      DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at      DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (run_id),
  -- store.TaskRunStore.CreateTaskRun's documented idempotency key:
  -- "(workflowRunID, parentRunID, scope, taskName)"
  UNIQUE KEY uk_idempotent (workflow_run_id, parent_run_id, scope, task_name),
  KEY idx_parent (workflow_run_id, parent_run_id),
  KEY idx_status_deadline (status, deadline)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- +goose Down
DROP TABLE IF EXISTS aether_task_runs;
DROP TABLE IF EXISTS aether_workflow_runs;
