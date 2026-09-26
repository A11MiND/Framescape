package jobsvc

import (
	"context"
	"fmt"

	"aigc-platform/internal/infra/persistence"
	"aigc-platform/internal/pkg/apperr"
)

// specAssetIDs is every asset a request names directly.
func specAssetIDs(spec Spec) []string {
	ids := []string{spec.SourceImageAssetID, spec.SourceVideoAssetID, spec.FirstFrameAssetID, spec.LastFrameAssetID}
	ids = append(ids, spec.ReferenceImageAssetIDs...)
	ids = append(ids, spec.ReferenceVideoAssetIDs...)
	ids = append(ids, spec.ReferenceAudioAssetIDs...)
	return ids
}

// checkAssetsOwned rejects a request naming an asset that is not the
// caller's live asset. Executors resolve asset ids without knowing the user,
// so this is where ownership is enforced; it runs before any planning call
// is paid for.
func (s *Service) checkAssetsOwned(ctx context.Context, userID uint64, ids []string) error {
	want := map[string]bool{}
	for _, id := range ids {
		if id != "" {
			want[id] = true
		}
	}
	if len(want) == 0 {
		return nil
	}
	list := make([]string, 0, len(want))
	for id := range want {
		list = append(list, id)
	}
	var n int64
	if err := s.db.WithContext(ctx).Model(&persistence.Asset{}).
		Where("biz_id IN ? AND user_id = ? AND deleted_at IS NULL", list, userID).Count(&n).Error; err != nil {
		return fmt.Errorf("check asset ownership: %w", err)
	}
	if int(n) != len(list) {
		return apperr.New("reference_unavailable", "a referenced asset is missing, deleted or not yours")
	}
	return nil
}
