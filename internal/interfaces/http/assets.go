package httpapi

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"aigc-platform/internal/infra/persistence"
)

// handleGetAsset is the W1 subset of F2.4/F2.5 (full asset library — filters,
// pagination, "以此再生成" — lands in W3+). For now it exists so the Studio
// page can render what the mock/minimax executor actually produced instead
// of a client-side placeholder.
func (s *Server) handleGetAsset(c *gin.Context) {
	var a persistence.Asset
	if err := s.db.Where("biz_id = ? AND user_id = ?", c.Param("bizID"), userID(c)).First(&a).Error; err != nil {
		c.JSON(http.StatusNotFound, errBody("not_found", "asset not found"))
		return
	}
	c.JSON(http.StatusOK, assetToJSON(a))
}

// handleListAssets is F2.4's list surface, minus the moderation/soft-delete
// filters that don't matter yet at POC scale: paged newest-first, optional
// ?type=image|video. Backs both the character-creation ref-image picker
// (F3.1 requires ref_asset_ids to already exist — there's no raw upload
// endpoint by design, see PRD §11) and the asset library page.
func (s *Server) handleListAssets(c *gin.Context) {
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "60"))
	if err != nil || limit <= 0 || limit > 200 {
		limit = 60
	}
	q := s.db.WithContext(c.Request.Context()).
		Where("user_id = ? AND deleted_at IS NULL", userID(c))
	if t := c.Query("type"); t != "" {
		q = q.Where("type = ?", t)
	}
	var rows []persistence.Asset
	if err := q.Order("id DESC").Limit(limit).Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "list assets"))
		return
	}
	out := make([]gin.H, 0, len(rows))
	for _, a := range rows {
		out = append(out, assetToJSON(a))
	}
	c.JSON(http.StatusOK, gin.H{"assets": out})
}

func assetToJSON(a persistence.Asset) gin.H {
	return gin.H{
		"biz_id":     a.BizID,
		"type":       a.Type,
		"public_url": a.PublicURL,
		"mime":       a.Mime,
		"width":      a.Width,
		"height":     a.Height,
	}
}
