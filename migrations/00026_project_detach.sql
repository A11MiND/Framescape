-- +goose Up
-- Deleting a project now detaches its assets, jobs and characters instead of
-- leaving them pointing at a hidden project; existing dangling references
-- are cleared the same way.
ALTER TABLE characters ADD KEY idx_project (project_id);

UPDATE assets a JOIN projects p ON p.id = a.project_id SET a.project_id = NULL WHERE p.deleted_at IS NOT NULL;
UPDATE jobs j JOIN projects p ON p.id = j.project_id SET j.project_id = NULL WHERE p.deleted_at IS NOT NULL;
UPDATE characters c JOIN projects p ON p.id = c.project_id SET c.project_id = NULL WHERE p.deleted_at IS NOT NULL;

-- +goose Down
ALTER TABLE characters DROP KEY idx_project;
