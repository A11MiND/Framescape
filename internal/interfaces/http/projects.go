package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"aigc-platform/internal/infra/persistence"
	"aigc-platform/internal/pkg/id"
)

const (
	maxProjectNameChars = 64
	maxProjectDescChars = 800
)

type createProjectRequest struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
}

func validProject(name, desc string) bool {
	n := utf8.RuneCountInString(strings.TrimSpace(name))
	return n > 0 && n <= maxProjectNameChars && utf8.RuneCountInString(desc) <= maxProjectDescChars
}

func (s *Server) handleCreateProject(c *gin.Context) {
	var req createProjectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errBody("bad_request", err.Error()))
		return
	}
	if !validProject(req.Name, req.Description) {
		c.JSON(http.StatusUnprocessableEntity, errBody("invalid_project", "name must be 1..64 characters and description at most 800"))
		return
	}
	row := persistence.Project{BizID: id.New(), UserID: userID(c), Name: strings.TrimSpace(req.Name), Description: req.Description}
	if err := s.db.WithContext(c.Request.Context()).Create(&row).Error; err != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "insert project"))
		return
	}
	c.JSON(http.StatusOK, projectToJSON(row, projectStats{}))
}

// projectStats is what a project card shows: real counts, the latest
// activity and up to four recent image previews.
type projectStats struct {
	Assets       int64
	Jobs         int64
	Characters   int64
	LastActivity *time.Time
	Covers       []string
}

func (s *Server) projectStats(ctx context.Context, ids []uint64) map[uint64]*projectStats {
	out := make(map[uint64]*projectStats, len(ids))
	for _, pid := range ids {
		out[pid] = &projectStats{Covers: []string{}}
	}
	if len(ids) == 0 {
		return out
	}
	type count struct {
		ProjectID uint64
		N         int64
		Last      *time.Time
	}
	collect := func(table string, apply func(*projectStats, count)) {
		var rows []count
		s.db.WithContext(ctx).Table(table).Select("project_id, COUNT(*) AS n, MAX(created_at) AS last").
			Where("project_id IN ? AND deleted_at IS NULL", ids).Group("project_id").Scan(&rows)
		for _, r := range rows {
			if st := out[r.ProjectID]; st != nil {
				apply(st, r)
				if r.Last != nil && (st.LastActivity == nil || r.Last.After(*st.LastActivity)) {
					st.LastActivity = r.Last
				}
			}
		}
	}
	collect("assets", func(st *projectStats, r count) { st.Assets = r.N })
	collect("jobs", func(st *projectStats, r count) { st.Jobs = r.N })
	collect("characters", func(st *projectStats, r count) { st.Characters = r.N })

	var covers []persistence.Asset
	s.db.WithContext(ctx).Raw(`SELECT project_id, biz_id, type, public_url, thumb_url FROM (
		SELECT project_id, biz_id, type, public_url, thumb_url, ROW_NUMBER() OVER (PARTITION BY project_id ORDER BY id DESC) AS rn
		FROM assets WHERE project_id IN ? AND deleted_at IS NULL AND type = 'image') t WHERE rn <= 4`, ids).Scan(&covers)
	for _, a := range covers {
		if a.ProjectID != nil && out[*a.ProjectID] != nil {
			out[*a.ProjectID].Covers = append(out[*a.ProjectID].Covers, thumbOrOriginal(a))
		}
	}
	return out
}

// handleListProjects is GET /api/v1/projects?q=: the user's projects with
// their counts, newest first.
func (s *Server) handleListProjects(c *gin.Context) {
	ctx := c.Request.Context()
	q := s.db.WithContext(ctx).Where("user_id = ? AND deleted_at IS NULL", userID(c))
	if term := strings.TrimSpace(c.Query("q")); term != "" {
		like := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(term) + "%"
		q = q.Where("name LIKE ? OR description LIKE ?", like, like)
	}
	var rows []persistence.Project
	if err := q.Order("id DESC").Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "list projects"))
		return
	}
	ids := make([]uint64, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	stats := s.projectStats(ctx, ids)
	out := make([]gin.H, 0, len(rows))
	for _, r := range rows {
		out = append(out, projectToJSON(r, *stats[r.ID]))
	}
	c.JSON(http.StatusOK, gin.H{"projects": out})
}

// handleGetProject is GET /api/v1/projects/{bizID}.
func (s *Server) handleGetProject(c *gin.Context) {
	ctx := c.Request.Context()
	var row persistence.Project
	if err := s.db.WithContext(ctx).Where("biz_id = ? AND user_id = ? AND deleted_at IS NULL", c.Param("bizID"), userID(c)).First(&row).Error; err != nil {
		c.JSON(http.StatusNotFound, errBody("not_found", "project not found"))
		return
	}
	c.JSON(http.StatusOK, projectToJSON(row, *s.projectStats(ctx, []uint64{row.ID})[row.ID]))
}

type updateProjectRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

func (s *Server) handleUpdateProject(c *gin.Context) {
	ctx := c.Request.Context()
	var req updateProjectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errBody("bad_request", err.Error()))
		return
	}
	var row persistence.Project
	if err := s.db.WithContext(ctx).Where("biz_id = ? AND user_id = ? AND deleted_at IS NULL", c.Param("bizID"), userID(c)).First(&row).Error; err != nil {
		c.JSON(http.StatusNotFound, errBody("not_found", "project not found"))
		return
	}
	if req.Name == nil && req.Description == nil {
		c.JSON(http.StatusBadRequest, errBody("bad_request", "no fields to update"))
		return
	}
	if req.Name != nil {
		row.Name = strings.TrimSpace(*req.Name)
	}
	if req.Description != nil {
		row.Description = *req.Description
	}
	if !validProject(row.Name, row.Description) {
		c.JSON(http.StatusUnprocessableEntity, errBody("invalid_project", "name must be 1..64 characters and description at most 800"))
		return
	}
	if err := s.db.WithContext(ctx).Model(&persistence.Project{}).Where("id = ?", row.ID).
		Updates(map[string]any{"name": row.Name, "description": row.Description}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "update project"))
		return
	}
	c.JSON(http.StatusOK, projectToJSON(row, *s.projectStats(ctx, []uint64{row.ID})[row.ID]))
}

// handleDeleteProject is DELETE /api/v1/projects/{bizID}. The project is
// hidden and its assets, jobs and characters are detached (they stay in the
// library, unassigned); nothing else is deleted. The response reports how
// many of each were detached.
func (s *Server) handleDeleteProject(c *gin.Context) {
	ctx := c.Request.Context()
	detached := map[string]int64{}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row persistence.Project
		if err := tx.Where("biz_id = ? AND user_id = ? AND deleted_at IS NULL", c.Param("bizID"), userID(c)).First(&row).Error; err != nil {
			return err
		}
		for _, table := range []string{"assets", "jobs", "characters"} {
			res := tx.Table(table).Where("project_id = ?", row.ID).Update("project_id", nil)
			if res.Error != nil {
				return res.Error
			}
			detached[table] = res.RowsAffected
		}
		return tx.Model(&persistence.Project{}).Where("id = ?", row.ID).Update("deleted_at", time.Now().UTC()).Error
	})
	if err == gorm.ErrRecordNotFound {
		c.JSON(http.StatusNotFound, errBody("not_found", "project not found"))
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "delete project"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"detached": detached})
}

func projectToJSON(row persistence.Project, st projectStats) gin.H {
	covers := st.Covers
	if covers == nil {
		covers = []string{}
	}
	return gin.H{
		"biz_id":           row.BizID,
		"name":             row.Name,
		"description":      row.Description,
		"created_at":       row.CreatedAt,
		"asset_count":      st.Assets,
		"job_count":        st.Jobs,
		"character_count":  st.Characters,
		"last_activity_at": st.LastActivity,
		"cover_urls":       covers,
	}
}
