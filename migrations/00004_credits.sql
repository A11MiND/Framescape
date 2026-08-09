-- +goose Up
-- W7 (DEV_PLAN.md §11): credit_ledger. credit_accounts already exists
-- (00001_init.sql) with balance/held/version but nothing has written to it
-- yet — this migration adds the ledger that makes every balance/held change
-- auditable and reconciliation-checkable (PRD §12.3).
--
-- Sign convention (deliberately NOT identical to §12.3's literal pseudocode
-- — see DEV_PLAN.md's W7 findings for the arithmetic bug that pseudocode
-- has): `amount` is always the true net delta to (balance+held) for that
-- one operation, so that `balance+held == SUM(ledger.amount)` is an actual
-- invariant, not just documentation. hold/refund only move value between
-- balance and held (net delta 0) so their ledger rows record amount=0 with
-- the real magnitude in `remark` for audit purposes; recharge (+N) and
-- commit (-actual, real spend leaving escrow permanently) are the only
-- entries with a nonzero amount.
CREATE TABLE credit_ledger (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id       BIGINT UNSIGNED NOT NULL,
  direction     VARCHAR(16)  NOT NULL,   -- recharge/hold/commit/refund
  amount        INT          NOT NULL,   -- signed net delta to (balance+held); see doc above
  balance_after INT          NOT NULL,
  held_after    INT          NOT NULL,
  ref_type      VARCHAR(32)  NOT NULL,   -- job/task_run/admin
  ref_id        VARCHAR(64)  NOT NULL,
  idem_key      VARCHAR(96)  NOT NULL,
  remark        VARCHAR(255) NOT NULL DEFAULT '',
  created_at    DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uk_idem (idem_key),
  KEY idx_user_time (user_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- +goose Down
DROP TABLE IF EXISTS credit_ledger;
