-- +goose Up
-- Email verification scaffold — same "接口搭好，密钥留空" shape as
-- migration 00013's Google/phone login: handleRegister only requires a
-- code when config.EmailProviderAPIKey() is set, so leaving that env var
-- unset (its default) keeps today's email+password registration working
-- exactly as before. Mirrors phone_verification_codes' own shape.
CREATE TABLE email_verification_codes (
  id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  email       VARCHAR(255) NOT NULL,
  code        VARCHAR(8) NOT NULL,
  expires_at  DATETIME(3) NOT NULL,
  consumed_at DATETIME(3) NULL,
  created_at  DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  KEY idx_email_expires (email, expires_at)
);

-- +goose Down
DROP TABLE email_verification_codes;
