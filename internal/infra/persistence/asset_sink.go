package persistence

import (
	"context"
	"encoding/json"
	"fmt"

	"gorm.io/gorm"

	"aigc-platform/internal/infra/executor/assetstore"
	"aigc-platform/internal/infra/storage"
	"aigc-platform/internal/pkg/id"
)

// GormAssetSink implements assetstore.Sink by writing directly to the
// `assets` table (and, for MaterializeBytes, uploading to MinIO first).
// This is deliberately the ONLY business-DB write an executor performs
// (PRD §6/§10: materialize is the one exception to "workers don't touch
// business state" — a generated file must become a citable asset before the
// executor returns, since downstream nodes reference it by asset_id).
type GormAssetSink struct {
	db      *gorm.DB
	objects *storage.Store // nil is fine for sinks that only ever call Materialize (e.g. mock)
}

func NewGormAssetSink(db *gorm.DB) *GormAssetSink {
	return &GormAssetSink{db: db}
}

func NewGormAssetSinkWithStorage(db *gorm.DB, objects *storage.Store) *GormAssetSink {
	return &GormAssetSink{db: db, objects: objects}
}

func (s *GormAssetSink) PublicURL(ctx context.Context, bizID string) (string, error) {
	var url string
	err := s.db.WithContext(ctx).Model(&Asset{}).Where("biz_id = ?", bizID).Pluck("public_url", &url).Error
	if err != nil {
		return "", fmt.Errorf("look up asset %q: %w", bizID, err)
	}
	if url == "" {
		return "", fmt.Errorf("asset %q has no public_url", bizID)
	}
	return url, nil
}

func (s *GormAssetSink) Materialize(ctx context.Context, a assetstore.NewAsset) (string, error) {
	return s.insertRow(ctx, a.UserID, a.ProjectID, a.Type, a.Source, a.FromTaskRunID,
		a.StorageKey, a.PublicURL, a.Mime, a.Width, a.Height, a.DurationMs, a.SizeBytes, a.ResolutionTag, a.Meta)
}

func (s *GormAssetSink) MaterializeBytes(ctx context.Context, a assetstore.NewAssetBytes) (string, error) {
	if s.objects == nil {
		return "", fmt.Errorf("MaterializeBytes: no object storage configured")
	}
	bizID := id.New()
	key := fmt.Sprintf("%s/%s.%s", a.Type, bizID, a.Ext)
	publicURL, err := s.objects.Put(ctx, key, a.Body, a.SizeBytes, a.Mime)
	if err != nil {
		return "", fmt.Errorf("upload to object storage: %w", err)
	}
	return s.insertRowWithID(ctx, bizID, a.UserID, a.ProjectID, a.Type, a.Source, a.FromTaskRunID,
		key, publicURL, a.Mime, a.Width, a.Height, a.DurationMs, a.SizeBytes, a.ResolutionTag, a.Meta)
}

func (s *GormAssetSink) insertRow(ctx context.Context, userID uint64, projectID *uint64, typ, source, fromTaskRunID,
	storageKey, publicURL, mime string, width, height, durationMs int, sizeBytes int64, resolutionTag string, meta map[string]any) (string, error) {
	return s.insertRowWithID(ctx, id.New(), userID, projectID, typ, source, fromTaskRunID,
		storageKey, publicURL, mime, width, height, durationMs, sizeBytes, resolutionTag, meta)
}

func (s *GormAssetSink) insertRowWithID(ctx context.Context, bizID string, userID uint64, projectID *uint64, typ, source, fromTaskRunID,
	storageKey, publicURL, mime string, width, height, durationMs int, sizeBytes int64, resolutionTag string, meta map[string]any) (string, error) {
	var metaJSON []byte
	if meta != nil {
		b, err := json.Marshal(meta)
		if err != nil {
			return "", fmt.Errorf("marshal asset meta: %w", err)
		}
		metaJSON = b
	}
	row := Asset{
		BizID:            bizID,
		UserID:           userID,
		ProjectID:        projectID,
		Type:             typ,
		Source:           source,
		FromTaskRunID:    fromTaskRunID,
		StorageKey:       storageKey,
		PublicURL:        publicURL,
		Mime:             mime,
		Width:            width,
		Height:           height,
		DurationMs:       durationMs,
		SizeBytes:        sizeBytes,
		ResolutionTag:    resolutionTag,
		Meta:             metaJSON,
		ModerationStatus: "pending",
	}
	if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
		return "", fmt.Errorf("insert asset: %w", err)
	}
	return bizID, nil
}
