package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"aigc-platform/internal/application/jobsvc"
	"aigc-platform/internal/domain/capability"
	"aigc-platform/internal/domain/comic"
	"aigc-platform/internal/pkg/config"
)

// handleGetCapabilities is GET /api/v1/capabilities: every limit the backend
// enforces and which providers and sign-in methods actually work on this
// deployment, so clients never hard-code or guess them. Public: the login
// page and guest studio need it before sign-in.
func (s *Server) handleGetCapabilities(c *gin.Context) {
	openAI := jobsvc.OpenAIComicEnabled()
	c.Header("Cache-Control", "public, max-age=60")
	c.JSON(http.StatusOK, gin.H{
		"comic": gin.H{
			"openai_enabled": openAI, "model": config.OpenAIImageModel(),
			"max_title_chars": comic.MaxTitleChars, "max_brief_chars": comic.MaxBriefChars,
			"max_composed_chars": comic.MaxComposedChars, "max_reference_label_chars": comic.MaxReferenceLabelChars,
			"max_context_chars": comic.MaxContextChars, "max_background_chars": comic.MaxBackgroundChars,
			"max_references": comic.MaxReferences, "max_layers": comic.MaxLayers,
			"output": gin.H{"width": 1536, "height": 1024, "format": "png", "quality": "high"},
		},
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
		// Which providers can run here; access to OpenAI is additionally
		// gated per user (GET /me entitlements).
		"providers": gin.H{
			"minimax": gin.H{"enabled": config.MiniMaxAPIKey() != "", "video_model": "MiniMax-H3", "image_model": "image-01"},
			"openai":  gin.H{"enabled": openAI, "image_model": config.OpenAIImageModel(), "entitlement": "openai_image"},
			"gemini":  gin.H{"enabled": config.GeminiVertexProjectID() != "", "image_model": config.GeminiImageModel()},
		},
		"auth": gin.H{
			"email_password":     true,
			"email_verification": emailAvailable(),
			"phone_sms":          smsAvailable(),
			"google":             config.GoogleClientID() != "",
			"guest_trial":        config.MiniMaxAPIKey() != "",
		},
	})
}
