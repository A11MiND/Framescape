-- +goose Up
-- Satellite-retry provenance (§the artifact's "現在還缺什麼" node-retry
-- gap): Aether's own Engine port only exposes Submit/Get/Resume/Cancel —
-- no way to re-trigger a single already-terminal task in place without
-- reopening the vendored engine's own one-way Phase invariant (see
-- third_party/aether/engine.go's doc). jobsvc.RetryNode instead resubmits
-- just the one failed leaf task as a brand new, ordinary one-task Job —
-- these three columns are the only new state needed, since everything
-- else (credits, SSE, job_nodes projection) is the existing Job pipeline
-- working unmodified. Nullable: only satellite retry jobs ever set them.
ALTER TABLE jobs
  ADD COLUMN retry_of_job_id     BIGINT UNSIGNED NULL AFTER project_id,
  ADD COLUMN retry_of_node_name  VARCHAR(64)     NULL AFTER retry_of_job_id,
  ADD COLUMN retry_of_loop_index INT             NULL AFTER retry_of_node_name,
  ADD KEY idx_retry_of_job (retry_of_job_id);

-- +goose Down
ALTER TABLE jobs
  DROP KEY idx_retry_of_job,
  DROP COLUMN retry_of_job_id,
  DROP COLUMN retry_of_node_name,
  DROP COLUMN retry_of_loop_index;
