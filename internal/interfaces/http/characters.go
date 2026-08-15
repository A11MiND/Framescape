package httpapi

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"aigc-platform/internal/infra/persistence"
	"aigc-platform/internal/pkg/id"
)

// createCharacterRequest is F3.1: name + description + 1-3 reference images
// + a fixed seed (the actual consistency lever, PRD §3.1).
type createCharacterRequest struct {
	Name        string   `json:"name" binding:"required"`
	Description string   `json:"description"`
	RefAssetIDs []string `json:"ref_asset_ids" binding:"required,min=1,max=3"`
	Seed        int64    `json:"seed" binding:"required"`
}

func (s *Server) handleCreateCharacter(c *gin.Context) {
	var req createCharacterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errBody("bad_request", err.Error()))
		return
	}
	refJSON, err := json.Marshal(req.RefAssetIDs)
	if err != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "marshal ref_asset_ids"))
		return
	}
	row := persistence.Character{
		BizID:       id.New(),
		UserID:      userID(c),
		Name:        req.Name,
		Description: req.Description,
		RefAssetIDs: refJSON,
		Seed:        req.Seed,
	}
	if err := s.db.WithContext(c.Request.Context()).Create(&row).Error; err != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "insert character"))
		return
	}
	c.JSON(http.StatusOK, characterToJSON(row))
}

func (s *Server) handleListCharacters(c *gin.Context) {
	var rows []persistence.Character
	err := s.db.WithContext(c.Request.Context()).
		Where("user_id = ? AND deleted_at IS NULL", userID(c)).
		Order("id DESC").Find(&rows).Error
	if err != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "list characters"))
		return
	}
	out := make([]gin.H, 0, len(rows))
	for _, r := range rows {
		out = append(out, characterToJSON(r))
	}
	c.JSON(http.StatusOK, gin.H{"characters": out})
}

// updateCharacterRequest's fields are all pointers so a PATCH only touches
// what the caller actually sent — a request that omits `seed` must leave
// the stored seed untouched, not zero it out.
type updateCharacterRequest struct {
	Name        *string   `json:"name"`
	Description *string   `json:"description"`
	RefAssetIDs *[]string `json:"ref_asset_ids"`
	Seed        *int64    `json:"seed"`
}

func (s *Server) handleUpdateCharacter(c *gin.Context) {
	var req updateCharacterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errBody("bad_request", err.Error()))
		return
	}
	if req.RefAssetIDs != nil && (len(*req.RefAssetIDs) < 1 || len(*req.RefAssetIDs) > 3) {
		c.JSON(http.StatusBadRequest, errBody("bad_request", "ref_asset_ids must have 1-3 entries"))
		return
	}

	updates := map[string]any{}
	if req.Name != nil {
		updates["name"] = *req.Name
	}
	if req.Description != nil {
		updates["description"] = *req.Description
	}
	if req.RefAssetIDs != nil {
		refJSON, err := json.Marshal(*req.RefAssetIDs)
		if err != nil {
			c.JSON(http.StatusInternalServerError, errBody("internal", "marshal ref_asset_ids"))
			return
		}
		updates["ref_asset_ids"] = refJSON
	}
	if req.Seed != nil {
		updates["seed"] = *req.Seed
	}
	if len(updates) == 0 {
		c.JSON(http.StatusBadRequest, errBody("bad_request", "no fields to update"))
		return
	}

	res := s.db.WithContext(c.Request.Context()).Model(&persistence.Character{}).
		Where("biz_id = ? AND user_id = ? AND deleted_at IS NULL", c.Param("bizID"), userID(c)).
		Updates(updates)
	if res.Error != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "update character"))
		return
	}
	if res.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, errBody("not_found", "character not found"))
		return
	}

	var row persistence.Character
	if err := s.db.WithContext(c.Request.Context()).
		Where("biz_id = ? AND user_id = ?", c.Param("bizID"), userID(c)).First(&row).Error; err != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "reload character"))
		return
	}
	c.JSON(http.StatusOK, characterToJSON(row))
}

// handleDeleteCharacter soft-deletes, same shape as handleDeleteAsset (a
// repeat DELETE is a no-op 404, not an error).
func (s *Server) handleDeleteCharacter(c *gin.Context) {
	now := time.Now()
	res := s.db.Model(&persistence.Character{}).
		Where("biz_id = ? AND user_id = ? AND deleted_at IS NULL", c.Param("bizID"), userID(c)).
		Update("deleted_at", now)
	if res.Error != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "delete character"))
		return
	}
	if res.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, errBody("not_found", "character not found"))
		return
	}
	c.Status(http.StatusNoContent)
}

func characterToJSON(row persistence.Character) gin.H {
	var refs []string
	_ = json.Unmarshal(row.RefAssetIDs, &refs)
	return gin.H{
		"biz_id":        row.BizID,
		"name":          row.Name,
		"description":   row.Description,
		"ref_asset_ids": refs,
		"seed":          row.Seed,
	}
}
