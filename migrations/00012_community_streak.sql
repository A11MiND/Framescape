-- +goose Up
-- Daily-publish streak rewards (product ask, confirmed rules): publishing
-- at least one asset to the community on a calendar day extends a running
-- streak; missing a day resets it to 0. Reaching 3/10/30 consecutive days
-- grants a one-time credit bonus (10/50/100) — "one-time" per occurrence,
-- where re-earning the 3-day bonus specifically requires the streak to
-- have broken and been rebuilt from scratch (an unbroken 30-day streak
-- only ever crosses day 3 once, not every 3rd day), and both the 3-day and
-- 10-day bonuses are additionally capped per calendar month (4x and 1x)
-- to bound how much an aggressive break-and-restart pattern can farm.
--
-- Two tables rather than reusing credit_ledger for the cap check: the cap
-- queries need to count "how many times milestone X was awarded this
-- calendar month for this user", which credit_ledger could only answer by
-- parsing its free-form JSON remark — a dedicated, indexed table is the
-- straightforward way to keep that query correct and fast. The actual
-- credit grant still goes through creditsvc (community_streak_rewards
-- here is the cap/audit record, not the ledger).
CREATE TABLE community_streaks (
  user_id           BIGINT UNSIGNED NOT NULL PRIMARY KEY,
  current_streak    INT NOT NULL DEFAULT 0,
  last_publish_date DATE NULL,
  updated_at        DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3)
);

CREATE TABLE community_streak_rewards (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  user_id       BIGINT UNSIGNED NOT NULL,
  milestone     INT NOT NULL,
  credits       INT NOT NULL,
  awarded_date  DATE NOT NULL,
  created_at    DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  KEY idx_user_milestone_date (user_id, milestone, awarded_date)
);

-- +goose Down
DROP TABLE community_streak_rewards;
DROP TABLE community_streaks;
