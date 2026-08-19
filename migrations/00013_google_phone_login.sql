-- +goose Up
-- Scaffolding for Google login and phone-number registration (product ask:
-- "接口搭好，密钥留空" — build the shape now, real provider credentials
-- land later via config.GoogleClientID/SMSAPIKey). email+password_hash both
-- go nullable since a Google- or phone-only account has neither; MySQL's
-- unique index already permits any number of NULL rows, so this doesn't
-- weaken the existing "one account per email" guarantee for accounts that
-- do have one.
ALTER TABLE users
  MODIFY COLUMN email VARCHAR(255) NULL,
  MODIFY COLUMN password_hash VARCHAR(255) NULL,
  ADD COLUMN phone VARCHAR(32) NULL AFTER email,
  ADD COLUMN google_sub VARCHAR(64) NULL AFTER phone,
  ADD UNIQUE KEY idx_phone (phone),
  ADD UNIQUE KEY idx_google_sub (google_sub);

-- One-time OTP codes for phone login/registration — a code is consumed
-- (consumed_at set) on first successful verify so it can't be replayed,
-- and expires_at bounds how long a sent code stays valid regardless.
CREATE TABLE phone_verification_codes (
  id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  phone       VARCHAR(32) NOT NULL,
  code        VARCHAR(8) NOT NULL,
  expires_at  DATETIME(3) NOT NULL,
  consumed_at DATETIME(3) NULL,
  created_at  DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  KEY idx_phone_expires (phone, expires_at)
);

-- +goose Down
DROP TABLE phone_verification_codes;
ALTER TABLE users
  DROP KEY idx_google_sub,
  DROP KEY idx_phone,
  DROP COLUMN google_sub,
  DROP COLUMN phone,
  MODIFY COLUMN password_hash VARCHAR(255) NOT NULL,
  MODIFY COLUMN email VARCHAR(255) NOT NULL;
