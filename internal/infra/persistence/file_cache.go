package persistence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// GormFileCache implements minimax.FileCache against `provider_files`
// (F3.4/§9.2). Kept as its own small type — same rationale as
// GormAssetSink — rather than folding into it, since not every executor
// that materializes assets also needs the provider-file cache.
type GormFileCache struct {
	db *gorm.DB
}

func NewGormFileCache(db *gorm.DB) *GormFileCache {
	return &GormFileCache{db: db}
}

func (c *GormFileCache) assetIDFor(ctx context.Context, assetBizID string) (uint64, error) {
	var assetID uint64
	err := c.db.WithContext(ctx).Model(&Asset{}).Where("biz_id = ?", assetBizID).Pluck("id", &assetID).Error
	if err != nil {
		return 0, err
	}
	if assetID == 0 {
		return 0, fmt.Errorf("asset %q not found", assetBizID)
	}
	return assetID, nil
}

func (c *GormFileCache) Get(ctx context.Context, assetBizID string) (string, bool, error) {
	assetID, err := c.assetIDFor(ctx, assetBizID)
	if err != nil {
		return "", false, err
	}
	var row ProviderFile
	err = c.db.WithContext(ctx).
		Where("asset_id = ? AND provider_code = ?", assetID, "minimax").
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return row.FileID, true, nil
}

func (c *GormFileCache) Put(ctx context.Context, assetBizID string, fileID string, purpose string, expireAt time.Time) error {
	assetID, err := c.assetIDFor(ctx, assetBizID)
	if err != nil {
		return err
	}
	row := ProviderFile{
		AssetID:      assetID,
		ProviderCode: "minimax",
		FileID:       fileID,
		Purpose:      purpose,
		ExpireAt:     &expireAt,
	}
	return c.db.WithContext(ctx).Create(&row).Error
}
