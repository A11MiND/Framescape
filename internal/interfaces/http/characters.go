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
	ProjectID   string   `json:"project_id,omitempty"`
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
	var projectID *uint64
	if req.ProjectID != "" {
		resolved, ok := s.resolveProjectID(c.Request.Context(), userID(c), req.ProjectID)
		if !ok {
			c.JSON(http.StatusNotFound, errBody("not_found", "project not found"))
			return
		}
		projectID = &resolved
	}
	row := persistence.Character{
		BizID:       id.New(),
		UserID:      userID(c),
		ProjectID:   projectID,
		Name:        req.Name,
		Description: req.Description,
		RefAssetIDs: refJSON,
		Seed:        req.Seed,
	}
	if err := s.db.WithContext(c.Request.Context()).Create(&row).Error; err != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "insert character"))
		return
	}
	j := characterToJSON(row)
	j["project_id"] = req.ProjectID
	c.JSON(http.StatusOK, j)
}

func (s *Server) handleListCharacters(c *gin.Context) {
	q := s.db.WithContext(c.Request.Context()).
		Where("user_id = ? AND deleted_at IS NULL", userID(c))
	if pid := c.Query("project_id"); pid != "" {
		projectID, ok := s.resolveProjectID(c.Request.Context(), userID(c), pid)
		if !ok {
			c.JSON(http.StatusNotFound, errBody("not_found", "project not found"))
			return
		}
		q = q.Where("project_id = ?", projectID)
	}
	var rows []persistence.Character
	if err := q.Order("id DESC").Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "list characters"))
		return
	}

	projectIDs := make([]uint64, 0)
	seen := map[uint64]bool{}
	for _, r := range rows {
		if r.ProjectID != nil && !seen[*r.ProjectID] {
			seen[*r.ProjectID] = true
			projectIDs = append(projectIDs, *r.ProjectID)
		}
	}
	projectBizByID := make(map[uint64]string, len(projectIDs))
	if len(projectIDs) > 0 {
		var projects []persistence.Project
		_ = s.db.WithContext(c.Request.Context()).Where("id IN ?", projectIDs).Find(&projects).Error
		for _, p := range projects {
			projectBizByID[p.ID] = p.BizID
		}
	}

	out := make([]gin.H, 0, len(rows))
	for _, r := range rows {
		projectBizID := ""
		if r.ProjectID != nil {
			projectBizID = projectBizByID[*r.ProjectID]
		}
		j := characterToJSON(r)
		j["project_id"] = projectBizID
		out = append(out, j)
	}
	c.JSON(http.StatusOK, gin.H{"characters": out})
}

// updateCharacterRequest's fields are all pointers so a PATCH only touches
// what the caller actually sent — a request that omits `seed` must leave
// the stored seed untouched, not zero it out.
type updateCharacterRequest struct {
	Name         *string   `json:"name"`
	Description  *string   `json:"description"`
	RefAssetIDs  *[]string `json:"ref_asset_ids"`
	Seed         *int64    `json:"seed"`
	ProjectID    *string   `json:"project_id"`
	ClearProject bool      `json:"clear_project"`
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
	if req.ClearProject {
		updates["project_id"] = nil
	} else if req.ProjectID != nil {
		resolved, ok := s.resolveProjectID(c.Request.Context(), userID(c), *req.ProjectID)
		if !ok {
			c.JSON(http.StatusNotFound, errBody("not_found", "project not found"))
			return
		}
		updates["project_id"] = resolved
	}
	if len(updates) == 0 {
		c.JSON(http.StatusBadRequest, errBody("bad_request", "no fields to update"))
		return
	}

	// Existence is checked by the reload below, not by RowsAffected here:
	// MySQL's default (non-clientFoundRows) driver reports RowsAffected as
	// rows actually *changed*, not rows *matched* — a PATCH that resends
	// the same values a character already has (a no-op resave, or a retried
	// idempotent request) legitimately affects 0 rows despite matching one,
	// which would otherwise produce a false "not found". The reload's own
	// gorm.ErrRecordNotFound is the real existence signal.
	res := s.db.WithContext(c.Request.Context()).Model(&persistence.Character{}).
		Where("biz_id = ? AND user_id = ? AND deleted_at IS NULL", c.Param("bizID"), userID(c)).
		Updates(updates)
	if res.Error != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "update character"))
		return
	}

	var row persistence.Character
	if err := s.db.WithContext(c.Request.Context()).
		Where("biz_id = ? AND user_id = ? AND deleted_at IS NULL", c.Param("bizID"), userID(c)).First(&row).Error; err != nil {
		c.JSON(http.StatusNotFound, errBody("not_found", "character not found"))
		return
	}
	j := characterToJSON(row)
	if row.ProjectID != nil {
		var projectBizID string
		_ = s.db.WithContext(c.Request.Context()).Model(&persistence.Project{}).
			Select("biz_id").Where("id = ?", *row.ProjectID).Scan(&projectBizID).Error
		j["project_id"] = projectBizID
	}
	c.JSON(http.StatusOK, j)
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
