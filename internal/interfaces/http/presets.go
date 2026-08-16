package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"aigc-platform/internal/infra/persistence"
	"aigc-platform/internal/pkg/id"
)

// handleListPresets is F4.2's data source (card grid): system presets
// (owner_user_id IS NULL, seeded by migration) plus, once F4.5's
// "另存為我的預設" exists, the caller's own saved ones — both share the
// same shape so the frontend's carousel doesn't need two code paths.
func (s *Server) handleListPresets(c *gin.Context) {
	q := s.db.WithContext(c.Request.Context()).Model(&persistence.Preset{}).
		Where("owner_user_id IS NULL OR owner_user_id = ?", userID(c))
	if cat := c.Query("category"); cat != "" {
		q = q.Where("category = ?", cat)
	}
	var rows []persistence.Preset
	if err := q.Order("priority DESC, id ASC").Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "list presets"))
		return
	}
	out := make([]gin.H, 0, len(rows))
	for _, r := range rows {
		out = append(out, presetToJSON(r))
	}
	c.JSON(http.StatusOK, gin.H{"presets": out})
}

// createPresetRequest is F4.5's "另存為我的預設": the current Composer
// prompt fragment plus whichever style tag was selected, saved under the
// caller's own owner_user_id so it only ever shows up in their own carousel
// (never mixed into another user's, and never mistaken for a seeded system
// preset — handleDeletePreset's ownership check relies on that separation).
type createPresetRequest struct {
	Name           string `json:"name" binding:"required"`
	PromptFragment string `json:"prompt_fragment" binding:"required"`
	Category       string `json:"category"`
	StyleType      string `json:"style_type"`
}

func (s *Server) handleCreatePreset(c *gin.Context) {
	var req createPresetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errBody("bad_request", err.Error()))
		return
	}
	category := req.Category
	if category == "" {
		category = "style"
	}
	uid := userID(c)
	row := persistence.Preset{
		BizID:          id.New(),
		Category:       category,
		Name:           req.Name,
		PromptFragment: req.PromptFragment,
		StyleType:      req.StyleType,
		OwnerUserID:    &uid,
	}
	if err := s.db.WithContext(c.Request.Context()).Create(&row).Error; err != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "insert preset"))
		return
	}
	c.JSON(http.StatusOK, presetToJSON(row))
}

// handleDeletePreset only ever matches rows this caller owns — a seeded
// system preset has owner_user_id NULL, which "owner_user_id = ?" never
// matches, so this can't be used to delete shared presets no matter what
// bizID is passed.
func (s *Server) handleDeletePreset(c *gin.Context) {
	res := s.db.WithContext(c.Request.Context()).
		Where("biz_id = ? AND owner_user_id = ?", c.Param("bizID"), userID(c)).
		Delete(&persistence.Preset{})
	if res.Error != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "delete preset"))
		return
	}
	if res.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, errBody("not_found", "preset not found"))
		return
	}
	c.Status(http.StatusNoContent)
}

func presetToJSON(r persistence.Preset) gin.H {
	return gin.H{
		"biz_id":          r.BizID,
		"category":        r.Category,
		"name":            r.Name,
		"name_en":         r.NameEn,
		"cover_url":       r.CoverURL,
		"prompt_fragment": r.PromptFragment,
		"priority":        r.Priority,
		"style_type":      r.StyleType,
		"mine":            r.OwnerUserID != nil,
	}
}
