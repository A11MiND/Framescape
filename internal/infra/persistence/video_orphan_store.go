package persistence

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

// GormVideoOrphanStore implements minimax.OrphanTaskStore against
// `video_orphan_tasks` (migrations/00017) — same rationale as
// GormFileCache/GormAssetSink, its own small type rather than folding into
// either.
type GormVideoOrphanStore struct {
	db *gorm.DB
}

func NewGormVideoOrphanStore(db *gorm.DB) *GormVideoOrphanStore {
	return &GormVideoOrphanStore{db: db}
}

func (s *GormVideoOrphanStore) Put(ctx context.Context, taskRunID, minimaxTaskID string) error {
	row := VideoOrphanTask{TaskRunID: taskRunID, MinimaxTaskID: minimaxTaskID, CreatedAt: time.Now()}
	return s.db.WithContext(ctx).Create(&row).Error
}

func (s *GormVideoOrphanStore) Get(ctx context.Context, taskRunID string) (string, bool, error) {
	var row VideoOrphanTask
	err := s.db.WithContext(ctx).
		Where("task_run_id = ? AND resolved_at IS NULL", taskRunID).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return row.MinimaxTaskID, true, nil
}

func (s *GormVideoOrphanStore) Resolve(ctx context.Context, taskRunID string) error {
	now := time.Now()
	return s.db.WithContext(ctx).Model(&VideoOrphanTask{}).
		Where("task_run_id = ? AND resolved_at IS NULL", taskRunID).
		Update("resolved_at", now).Error
}
