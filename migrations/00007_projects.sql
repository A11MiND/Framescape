-- +goose Up
-- `assets`/`jobs`/`characters` have all carried a nullable `project_id`
-- column since 00001/00003 (§17's "现在预留但不实现" pattern — `assets`
-- even already has `idx_project (project_id, id)` sitting unused) but no
-- `projects` table ever existed to reference. This is that table, plus the
-- CRUD to manage it — scoped first to assets (the exact "資產庫的專案篩選、
-- 搜尋" gap the composer-blueprint artifact's §09 called out), not jobs/
-- characters, which stay unassigned until a real need shows up.
CREATE TABLE projects (
  id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  biz_id      CHAR(26)     NOT NULL,
  user_id     BIGINT UNSIGNED NOT NULL,
  name        VARCHAR(128) NOT NULL,
  description VARCHAR(512) NOT NULL DEFAULT '',
  deleted_at  DATETIME(3)  NULL,
  created_at  DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at  DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uk_biz (biz_id),
  KEY idx_user (user_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- +goose Down
DROP TABLE IF EXISTS projects;
