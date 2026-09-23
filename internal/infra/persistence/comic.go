package persistence

import "time"

type ComicDocument struct {
	ID        uint64 `gorm:"primaryKey"`
	BizID     string `gorm:"column:biz_id"`
	UserID    uint64 `gorm:"column:user_id"`
	Title     string
	Document  []byte `gorm:"type:json"`
	Version   int
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (ComicDocument) TableName() string { return "comic_documents" }
