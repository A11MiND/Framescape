-- +goose Up
-- §07's "英文界面下预设名称仍是中文" gap — presets.name was always a single
-- column, fine for prompt_fragment (already English, MiniMax-facing) but
-- not for name, which is chrome a user actually reads. User-saved "mine"
-- presets (handleCreatePreset) stay single-language on purpose — that's
-- the user's own free text, same as a Character's name, never auto-
-- translated — so this only backfills the 14 seeded system presets;
-- name_en stays '' for everything else and the frontend falls back to
-- `name` whenever it's empty.
ALTER TABLE presets ADD COLUMN name_en VARCHAR(64) NOT NULL DEFAULT '' AFTER name;

UPDATE presets SET name_en = 'Watercolor' WHERE biz_id = '01PRESETWATERCOLOR00000000';
UPDATE presets SET name_en = 'Manga' WHERE biz_id = '01PRESETMANGA0000000000000';
UPDATE presets SET name_en = 'Genki' WHERE biz_id = '01PRESETGENKI0000000000000';
UPDATE presets SET name_en = 'Medieval' WHERE biz_id = '01PRESETMEDIEVAL0000000000';
UPDATE presets SET name_en = 'Back to Back' WHERE biz_id = '01PRESETPOSEBACK2BACK00000';
UPDATE presets SET name_en = 'Low Angle' WHERE biz_id = '01PRESETPOSELOWANGLE000000';
UPDATE presets SET name_en = 'Rule of Thirds' WHERE biz_id = '01PRESETCOMPTHIRDS00000000';
UPDATE presets SET name_en = 'Rim Light' WHERE biz_id = '01PRESETLIGHTRIM0000000000';
UPDATE presets SET name_en = 'Golden Hour' WHERE biz_id = '01PRESETLIGHTGOLDEN0000000';
UPDATE presets SET name_en = 'Close-Up' WHERE biz_id = '01PRESETCAMERACLOSEUP00000';
UPDATE presets SET name_en = 'Centered' WHERE biz_id = '01PRESETCOMPCENTERED000000';
UPDATE presets SET name_en = 'Diagonal' WHERE biz_id = '01PRESETCOMPDIAGONAL000000';
UPDATE presets SET name_en = 'Wide Shot' WHERE biz_id = '01PRESETCAMERAWIDE00000000';
UPDATE presets SET name_en = 'Bird''s Eye' WHERE biz_id = '01PRESETCAMERABIRDEYE00000';

-- +goose Down
ALTER TABLE presets DROP COLUMN name_en;
