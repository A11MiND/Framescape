-- +goose Up
-- §07's preset library review: 10 seed presets (00003) never got cover
-- images (cover_url was always ''), which is why the Studio composer's
-- carousel and the /presets library page both only ever showed a "无封面"
-- placeholder — the schema/API always supported cover_url, there was just
-- no image data. Also: composition and camera only had 1 preset each,
-- thin enough to feel unfinished next to style's 4 — adding a couple more
-- to each rounds the library out to 14 across the same 5 categories.
UPDATE presets SET cover_url = '/preset-covers/style-watercolor.jpg' WHERE biz_id = '01PRESETWATERCOLOR00000000';
UPDATE presets SET cover_url = '/preset-covers/style-manga.jpg' WHERE biz_id = '01PRESETMANGA0000000000000';
UPDATE presets SET cover_url = '/preset-covers/style-genki.jpg' WHERE biz_id = '01PRESETGENKI0000000000000';
UPDATE presets SET cover_url = '/preset-covers/style-medieval.jpg' WHERE biz_id = '01PRESETMEDIEVAL0000000000';
UPDATE presets SET cover_url = '/preset-covers/pose-back2back.jpg' WHERE biz_id = '01PRESETPOSEBACK2BACK00000';
UPDATE presets SET cover_url = '/preset-covers/pose-lowangle.jpg' WHERE biz_id = '01PRESETPOSELOWANGLE000000';
UPDATE presets SET cover_url = '/preset-covers/composition-thirds.jpg' WHERE biz_id = '01PRESETCOMPTHIRDS00000000';
UPDATE presets SET cover_url = '/preset-covers/lighting-rim.jpg' WHERE biz_id = '01PRESETLIGHTRIM0000000000';
UPDATE presets SET cover_url = '/preset-covers/lighting-golden.jpg' WHERE biz_id = '01PRESETLIGHTGOLDEN0000000';
UPDATE presets SET cover_url = '/preset-covers/camera-closeup.jpg' WHERE biz_id = '01PRESETCAMERACLOSEUP00000';

INSERT INTO presets (biz_id, category, name, cover_url, prompt_fragment, priority, style_type) VALUES
  ('01PRESETCOMPCENTERED000000', 'composition', '居中构图', '/preset-covers/composition-centered.jpg', 'centered composition, symmetrical framing', 30, ''),
  ('01PRESETCOMPDIAGONAL000000', 'composition', '对角线构图', '/preset-covers/composition-diagonal.jpg', 'diagonal composition, dynamic leading lines', 30, ''),
  ('01PRESETCAMERAWIDE00000000', 'camera', '远景', '/preset-covers/camera-wide.jpg', 'wide shot, full body, environmental context', 20, ''),
  ('01PRESETCAMERABIRDEYE00000', 'camera', '俯拍', '/preset-covers/camera-birdseye.jpg', 'high angle shot, bird''s eye view', 20, '');

-- +goose Down
DELETE FROM presets WHERE biz_id IN (
  '01PRESETCOMPCENTERED000000', '01PRESETCOMPDIAGONAL000000',
  '01PRESETCAMERAWIDE00000000', '01PRESETCAMERABIRDEYE00000'
);
UPDATE presets SET cover_url = '' WHERE biz_id IN (
  '01PRESETWATERCOLOR00000000', '01PRESETMANGA0000000000000', '01PRESETGENKI0000000000000',
  '01PRESETMEDIEVAL0000000000', '01PRESETPOSEBACK2BACK00000', '01PRESETPOSELOWANGLE000000',
  '01PRESETCOMPTHIRDS00000000', '01PRESETLIGHTRIM0000000000', '01PRESETLIGHTGOLDEN0000000',
  '01PRESETCAMERACLOSEUP00000'
);
