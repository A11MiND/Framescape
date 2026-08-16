package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"aigc-platform/internal/domain/capability"
)

// handleGetCapabilities is GET /api/v1/capabilities (PRD §10.5/§13.2's
// "Capability Matrix... 前端启动拉取缓存"): the generation constraints the
// backend actually enforces, as one response instead of the frontend
// re-guessing them in its own hardcoded lists (videoSpec.ts's RATIO_VALUES,
// Studio.tsx's duration/batch-size <select> options all used to be
// independent copies of these same numbers — see capability package's own
// doc for the drift risk that created). Public, not behind requireAuth —
// PRD's own phrasing ("启动拉取缓存") implies this loads before login, same
// reasoning as F1.2's anonymous trial being reachable from Studio's guest
// state.
func (s *Server) handleGetCapabilities(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"image": gin.H{
			"max_n":            capability.ImageMaxN,
			"max_prompt_chars": capability.ImageMaxPromptChars,
		},
		"video": gin.H{
			"duration_min":     capability.VideoDurationMin,
			"duration_max":     capability.VideoDurationMax,
			"max_prompt_chars": capability.VideoMaxPromptChars,
			"resolutions":      capability.VideoResolutions,
			"ratios":           capability.VideoRatios,
		},
	})
}
