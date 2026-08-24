-- +goose Up
-- Backs the video-generation orphan-task recovery fix: internal/infra/
-- executor/minimax's video.go used to abandon a MiniMax task_id forever the
-- moment our own wait loop gave up (videoMaxWait, 25m), then create a brand
-- new one on Aether's retry — if the original task went on to succeed on
-- MiniMax's side (and had already been billed for), that result and its
-- cost were both silently wasted. Keyed by task_run_id: Aether reuses the
-- same TaskRun row across every retry of one node (engine.go's
-- onTaskCompleted retry path updates RetryCount in place rather than
-- allocating a new run), so it's already the correct unique identity for
-- "this node instance" with no risk of colliding across two different loop
-- iterations the way a bare task name could. The executor checks here
-- before creating a new task, recovering the prior one if it already
-- finished.
CREATE TABLE video_orphan_tasks (
  id                BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  task_run_id       VARCHAR(64)  NOT NULL,
  minimax_task_id   VARCHAR(128) NOT NULL,
  resolved_at       DATETIME     NULL,
  created_at        DATETIME     NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_task_run (task_run_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- +goose Down
DROP TABLE video_orphan_tasks;
