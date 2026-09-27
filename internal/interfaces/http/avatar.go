package httpapi

import (
	"errors"
	"net/http"

	"aigc-platform/internal/infra/persistence"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Return only an owned, live image. Deleting an avatar asset restores the default.
func (s *Server) avatarURL(c *gin.Context, user persistence.User) string {
	if user.AvatarAssetID == nil {
		return ""
	}
	var asset persistence.Asset
	if err := s.db.WithContext(c.Request.Context()).Where("biz_id = ? AND user_id = ? AND deleted_at IS NULL AND type = ?", *user.AvatarAssetID, user.ID, "image").First(&asset).Error; err != nil {
		return ""
	}
	return asset.PublicURL
}

// The client submits an asset ID, never an arbitrary URL or storage key.
func (s *Server) handleSetAvatar(c *gin.Context) {
	var req struct {
		AssetID *string `json:"asset_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errBody("bad_request", "invalid avatar"))
		return
	}
	uid := userID(c)
	err := s.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		if req.AssetID != nil && *req.AssetID != "" {
			var a persistence.Asset
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("biz_id = ? AND user_id = ? AND deleted_at IS NULL", *req.AssetID, uid).First(&a).Error; err != nil {
				return err
			}
			if a.Type != "image" || (a.Mime != "image/png" && a.Mime != "image/jpeg" && a.Mime != "image/webp") || a.SizeBytes <= 0 || a.SizeBytes > 5*1024*1024 || a.PublicURL == "" {
				return errInvalidAvatar
			}
		} else {
			req.AssetID = nil
		}
		return tx.Model(&persistence.User{}).Where("id = ?", uid).Update("avatar_asset_id", req.AssetID).Error
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, errBody("not_found", "avatar not found"))
		return
	}
	if errors.Is(err, errInvalidAvatar) {
		c.JSON(http.StatusBadRequest, errBody("bad_request", "invalid avatar image"))
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "save avatar"))
		return
	}
	s.handleMe(c)
}

var errInvalidAvatar = errors.New("invalid avatar")
