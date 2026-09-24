package persistence

import (
	"context"

	"gorm.io/gorm"
)

// EntitlementOpenAIImage grants OpenAI image generation (gray release).
const EntitlementOpenAIImage = "openai_image"

// Entitlements lists a user's granted entitlements.
func Entitlements(ctx context.Context, db *gorm.DB, userID uint64) ([]string, error) {
	var out []string
	err := db.WithContext(ctx).Table("user_entitlements").Where("user_id = ?", userID).Order("entitlement").Pluck("entitlement", &out).Error
	return out, err
}

// HasEntitlement reports whether the user was granted key.
func HasEntitlement(ctx context.Context, db *gorm.DB, userID uint64, key string) (bool, error) {
	var n int64
	err := db.WithContext(ctx).Table("user_entitlements").Where("user_id = ? AND entitlement = ?", userID, key).Count(&n).Error
	return n > 0, err
}

// UsersWithEntitlement returns which of userIDs hold key.
func UsersWithEntitlement(ctx context.Context, db *gorm.DB, userIDs []uint64, key string) (map[uint64]bool, error) {
	out := map[uint64]bool{}
	if len(userIDs) == 0 {
		return out, nil
	}
	var ids []uint64
	if err := db.WithContext(ctx).Table("user_entitlements").Where("user_id IN ? AND entitlement = ?", userIDs, key).Pluck("user_id", &ids).Error; err != nil {
		return nil, err
	}
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}

// SetEntitlement grants or revokes key.
func SetEntitlement(ctx context.Context, db *gorm.DB, userID uint64, key string, on bool, grantedBy uint64) error {
	if !on {
		return db.WithContext(ctx).Exec(`DELETE FROM user_entitlements WHERE user_id = ? AND entitlement = ?`, userID, key).Error
	}
	return db.WithContext(ctx).Exec(`INSERT INTO user_entitlements (user_id, entitlement, granted_by) VALUES (?, ?, NULLIF(?, 0))
		ON DUPLICATE KEY UPDATE entitlement = entitlement`, userID, key, grantedBy).Error
}
