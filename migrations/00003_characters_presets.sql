-- +goose Up
-- W4 (DEV_PLAN.md §8): character library (F3), preset library (F4),
-- provider_files (MiniMax file_id cache, F3.4/§9.2 — same-asset re-upload
-- avoided via the unique key on (asset_id, account_id)).

CREATE TABLE characters (
  id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  biz_id      CHAR(26)     NOT NULL,
  user_id     BIGINT UNSIGNED NOT NULL,
  project_id  BIGINT UNSIGNED NULL,
  name        VARCHAR(64)  NOT NULL,
  description VARCHAR(1024) NOT NULL DEFAULT '',
  ref_asset_ids JSON       NOT NULL, -- 1..3 image asset biz_ids (F3.1)
  seed        BIGINT       NOT NULL, -- fixed seed, the main consistency lever (§3.1)
  deleted_at  DATETIME(3)  NULL,
  created_at  DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at  DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uk_biz (biz_id),
  KEY idx_user (user_id, deleted_at, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE presets (
  id              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  biz_id          CHAR(26)     NOT NULL,
  category        VARCHAR(16)  NOT NULL, -- style/pose/composition/lighting/camera (F4.1)
  name            VARCHAR(64)  NOT NULL,
  cover_url       VARCHAR(1024) NOT NULL DEFAULT '',
  prompt_fragment VARCHAR(512) NOT NULL DEFAULT '',
  priority        INT          NOT NULL DEFAULT 0, -- higher survives length-budget trimming first (§5.3 step 5)
  style_type      VARCHAR(16)  NOT NULL DEFAULT '', -- maps to image-01-live's style_type (F4.4): 漫画/元气/中世纪/水彩
  owner_user_id   BIGINT UNSIGNED NULL, -- reserved, unused (§17): F4.5 user-defined presets is P2, not built in POC
  created_at      DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uk_biz (biz_id),
  KEY idx_category (category)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE provider_files (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  asset_id      BIGINT UNSIGNED NOT NULL,
  provider_code VARCHAR(32)  NOT NULL DEFAULT 'minimax',
  account_id    BIGINT UNSIGNED NOT NULL DEFAULT 0, -- POC has one MiniMax account; column kept for §17's multi-account evolution
  file_id       VARCHAR(128) NOT NULL,              -- used as mm_file://{file_id}
  purpose       VARCHAR(32)  NOT NULL DEFAULT '',
  expire_at     DATETIME(3)  NULL,                  -- MiniMax: video_generation_input purpose expires in 7 days
  created_at    DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uk_asset_account (asset_id, account_id),
  KEY idx_expire (expire_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Seed presets (F4.2's card grid needs something to show; a handful per
-- category is enough for POC — not exhaustive, easy to add more later).
INSERT INTO presets (biz_id, category, name, prompt_fragment, priority, style_type) VALUES
  ('01PRESETWATERCOLOR00000000', 'style', '水彩', 'watercolor style, soft edges, paper texture', 50, '水彩'),
  ('01PRESETMANGA0000000000000', 'style', '漫画', 'manga style, bold ink lines, screentone shading', 50, '漫画'),
  ('01PRESETGENKI0000000000000', 'style', '元气', 'genki style, bright cheerful colors, dynamic pose', 50, '元气'),
  ('01PRESETMEDIEVAL0000000000', 'style', '中世纪', 'medieval illustration style, muted earthy palette', 50, '中世纪'),
  ('01PRESETPOSEBACK2BACK00000', 'pose', '背靠背', 'back to back pose', 40, ''),
  ('01PRESETPOSELOWANGLE000000', 'pose', '低角度', 'low angle shot', 40, ''),
  ('01PRESETCOMPTHIRDS00000000', 'composition', '三分构图', 'rule of thirds composition', 30, ''),
  ('01PRESETLIGHTRIM0000000000', 'lighting', '边缘光', 'rim lighting, dramatic backlight', 30, ''),
  ('01PRESETLIGHTGOLDEN0000000', 'lighting', '黄金时刻', 'golden hour lighting, warm tones', 30, ''),
  ('01PRESETCAMERACLOSEUP00000', 'camera', '特写', 'close-up shot', 20, '');

-- +goose Down
DROP TABLE IF EXISTS provider_files;
DROP TABLE IF EXISTS presets;
DROP TABLE IF EXISTS characters;
