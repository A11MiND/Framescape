-- +goose Up
CREATE TABLE comic_documents (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    biz_id VARCHAR(64) NOT NULL,
    user_id BIGINT UNSIGNED NOT NULL,
    title VARCHAR(128) NOT NULL,
    document JSON NOT NULL,
    version INT NOT NULL DEFAULT 1,
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    UNIQUE KEY uk_comic_biz (biz_id),
    KEY idx_comic_user_updated (user_id, updated_at)
);

-- +goose Down
DROP TABLE comic_documents;
