-- +goose Up
-- Backs the compact per-day publish heatmap (§07 gap: a full calendar view
-- was tried once in migration 00012's era and reverted for being enormous
-- on the page — grid-cols-7 + aspect-square cells stretched to fill
-- max-w-5xl, not a rejection of the underlying idea. This is that same
-- per-day record, kept minimal: existence of a row IS the "published that
-- day" fact, nothing else needs storing — RecordPublish already computes
-- and persists the running streak length separately in community_streaks.
CREATE TABLE community_publish_log (
  user_id      BIGINT UNSIGNED NOT NULL,
  publish_date DATE NOT NULL,
  PRIMARY KEY (user_id, publish_date)
);

-- +goose Down
DROP TABLE community_publish_log;
