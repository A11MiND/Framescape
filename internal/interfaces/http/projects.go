package httpapi

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"aigc-platform/internal/infra/persistence"
	"aigc-platform/internal/pkg/id"
)

// createProjectRequest is §the composer-blueprint artifact's "/projects
// CRUD" gap: just a name/description container assets can be filed under
// (F2.4's "資產庫的專案篩選、搜尋" — the one thing that gap explicitly
// depended on this for). jobs/characters keep their own unused project_id
// column for now; wiring those in is a separate, later step.
type createProjectRequest struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
}

func (s *Server) handleCreateProject(c *gin.Context) {
	var req createProjectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errBody("bad_request", err.Error()))
		return
	}
	row := persistence.Project{
		BizID:       id.New(),
		UserID:      userID(c),
		Name:        req.Name,
		Description: req.Description,
	}
	if err := s.db.WithContext(c.Request.Context()).Create(&row).Error; err != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "insert project"))
		return
	}
	c.JSON(http.StatusOK, projectToJSON(row))
}

func (s *Server) handleListProjects(c *gin.Context) {
	var rows []persistence.Project
	err := s.db.WithContext(c.Request.Context()).
		Where("user_id = ? AND deleted_at IS NULL", userID(c)).
		Order("id DESC").Find(&rows).Error
	if err != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "list projects"))
		return
	}
	out := make([]gin.H, 0, len(rows))
	for _, r := range rows {
		out = append(out, projectToJSON(r))
	}
	c.JSON(http.StatusOK, gin.H{"projects": out})
}

// updateProjectRequest's fields are pointers so a PATCH only touches what
// the caller actually sent, same shape as updateCharacterRequest.
type updateProjectRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

func (s *Server) handleUpdateProject(c *gin.Context) {
	var req updateProjectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errBody("bad_request", err.Error()))
		return
	}
	updates := map[string]any{}
	if req.Name != nil {
		updates["name"] = *req.Name
	}
	if req.Description != nil {
		updates["description"] = *req.Description
	}
	if len(updates) == 0 {
		c.JSON(http.StatusBadRequest, errBody("bad_request", "no fields to update"))
		return
	}

	// Existence is checked by the reload below, not by RowsAffected here —
	// see handleUpdateCharacter's identical comment for why (MySQL's
	// default driver reports RowsAffected as rows changed, not matched, so
	// a no-op resave would otherwise false-404).
	res := s.db.WithContext(c.Request.Context()).Model(&persistence.Project{}).
		Where("biz_id = ? AND user_id = ? AND deleted_at IS NULL", c.Param("bizID"), userID(c)).
		Updates(updates)
	if res.Error != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "update project"))
		return
	}

	var row persistence.Project
	if err := s.db.WithContext(c.Request.Context()).
		Where("biz_id = ? AND user_id = ? AND deleted_at IS NULL", c.Param("bizID"), userID(c)).First(&row).Error; err != nil {
		c.JSON(http.StatusNotFound, errBody("not_found", "project not found"))
		return
	}
	c.JSON(http.StatusOK, projectToJSON(row))
}

// handleDeleteProject soft-deletes, same shape as handleDeleteCharacter/
// handleDeleteAsset — a repeat DELETE is a no-op 404, not an error. Assets
// already filed under this project keep their project_id (a dangling
// reference to a soft-deleted project, same as any other soft-delete in
// this schema); handleListAssets's own ?project_id= filter still returns
// them by id, it just won't appear in handleListProjects anymore.
func (s *Server) handleDeleteProject(c *gin.Context) {
	now := time.Now()
	res := s.db.Model(&persistence.Project{}).
		Where("biz_id = ? AND user_id = ? AND deleted_at IS NULL", c.Param("bizID"), userID(c)).
		Update("deleted_at", now)
	if res.Error != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "delete project"))
		return
	}
	if res.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, errBody("not_found", "project not found"))
		return
	}
	c.Status(http.StatusNoContent)
}

func projectToJSON(row persistence.Project) gin.H {
	return gin.H{
		"biz_id":      row.BizID,
		"name":        row.Name,
		"description": row.Description,
		"created_at":  row.CreatedAt,
	}
}
