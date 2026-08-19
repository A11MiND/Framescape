-- +goose Up
-- Jobs (作业) had no way to remove a record from the history list — every
-- other user-owned entity (assets, characters, projects) already has a
-- deleted_at soft-delete column; jobs never got one. Soft, not hard: a job
-- row is also credit-ledger provenance (ref_id in credit_ledger points at
-- its biz_id), so hard-deleting it would orphan those rows.
ALTER TABLE jobs
  ADD COLUMN deleted_at DATETIME(3) NULL AFTER updated_at,
  ADD KEY idx_deleted_at (deleted_at);

-- +goose Down
ALTER TABLE jobs
  DROP KEY idx_deleted_at,
  DROP COLUMN deleted_at;
