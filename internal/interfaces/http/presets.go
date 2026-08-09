package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"aigc-platform/internal/infra/persistence"
)

// handleListPresets is F4.2's data source (card grid, seeded by migration —
// F4.5 user-defined presets is P2, not built).
func (s *Server) handleListPresets(c *gin.Context) {
	q := s.db.WithContext(c.Request.Context()).Model(&persistence.Preset{})
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
		out = append(out, gin.H{
			"biz_id":          r.BizID,
			"category":        r.Category,
			"name":            r.Name,
			"cover_url":       r.CoverURL,
			"prompt_fragment": r.PromptFragment,
			"priority":        r.Priority,
			"style_type":      r.StyleType,
		})
	}
	c.JSON(http.StatusOK, gin.H{"presets": out})
}
