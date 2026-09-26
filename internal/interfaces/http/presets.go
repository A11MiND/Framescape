package httpapi

import (
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"aigc-platform/internal/infra/persistence"
	"aigc-platform/internal/pkg/apperr"
	"aigc-platform/internal/pkg/id"
)

// PresetCategories are the preset kinds the library groups by (F4.1).
var PresetCategories = []string{"style", "pose", "composition", "lighting", "camera"}

const (
	presetNameMax      = 64
	presetFragmentMax  = 512
	presetStyleTypeMax = 16
)

// checkPresetFields trims and bounds a user preset's fields to what the
// table holds; nil fields are left alone.
func checkPresetFields(name, fragment, category, styleType *string) error {
	if name != nil {
		*name = strings.TrimSpace(*name)
		if *name == "" || utf8.RuneCountInString(*name) > presetNameMax {
			return apperr.New("text_length", "preset name must be 1-64 characters", "field", "name", "max", presetNameMax)
		}
	}
	if fragment != nil {
		*fragment = strings.TrimSpace(*fragment)
		if *fragment == "" || utf8.RuneCountInString(*fragment) > presetFragmentMax {
			return apperr.New("text_length", "prompt fragment must be 1-512 characters", "field", "prompt_fragment", "max", presetFragmentMax)
		}
	}
	if category != nil && !slices.Contains(PresetCategories, *category) {
		return apperr.New("bad_request", "unknown preset category")
	}
	if styleType != nil {
		*styleType = strings.TrimSpace(*styleType)
		if utf8.RuneCountInString(*styleType) > presetStyleTypeMax {
			return apperr.New("text_length", "style tag is too long", "field", "style_type", "max", presetStyleTypeMax)
		}
	}
	return nil
}

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
	if err := checkPresetFields(&req.Name, &req.PromptFragment, &category, &req.StyleType); err != nil {
		writeError(c, err, http.StatusBadRequest, "bad_request")
		return
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
type updatePresetRequest struct {
	Name           *string `json:"name"`
	PromptFragment *string `json:"prompt_fragment"`
	Category       *string `json:"category"`
	StyleType      *string `json:"style_type"`
}

// handleUpdatePreset edits one of the caller's own presets; system presets
// are read-only and answer 404 like another user's.
func (s *Server) handleUpdatePreset(c *gin.Context) {
	var req updatePresetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errBody("bad_request", err.Error()))
		return
	}
	if err := checkPresetFields(req.Name, req.PromptFragment, req.Category, req.StyleType); err != nil {
		writeError(c, err, http.StatusBadRequest, "bad_request")
		return
	}
	updates := map[string]any{}
	for col, v := range map[string]*string{"name": req.Name, "prompt_fragment": req.PromptFragment, "category": req.Category, "style_type": req.StyleType} {
		if v != nil {
			updates[col] = *v
		}
	}
	if len(updates) == 0 {
		c.JSON(http.StatusBadRequest, errBody("bad_request", "no fields to update"))
		return
	}
	ctx := c.Request.Context()
	var row persistence.Preset
	if err := s.db.WithContext(ctx).Where("biz_id = ? AND owner_user_id = ?", c.Param("bizID"), userID(c)).First(&row).Error; err != nil {
		c.JSON(http.StatusNotFound, errBody("not_found", "preset not found"))
		return
	}
	if err := s.db.WithContext(ctx).Model(&persistence.Preset{}).Where("id = ?", row.ID).Updates(updates).Error; err != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "update preset"))
		return
	}
	if err := s.db.WithContext(ctx).Where("id = ?", row.ID).First(&row).Error; err != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "reload preset"))
		return
	}
	c.JSON(http.StatusOK, presetToJSON(row))
}

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
