package httpapi

import (
	"aigc-platform/internal/application/upkeep"
	"aigc-platform/internal/application/workflows"
	"net/http"

	"github.com/gin-gonic/gin"

	"aigc-platform/internal/application/jobsvc"
	"aigc-platform/internal/domain/capability"
	"aigc-platform/internal/domain/comic"
	"aigc-platform/internal/infra/executor/openai"
	"aigc-platform/internal/pkg/config"
)

// handleGetCapabilities is GET /api/v1/capabilities: every limit the backend
// enforces and which providers and sign-in methods actually work on this
// deployment, so clients never hard-code or guess them. Public: the login
// page and guest studio need it before sign-in.
func (s *Server) handleGetCapabilities(c *gin.Context) {
	openAI := jobsvc.OpenAIImageEnabled()
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
		// Deleted assets stay restorable in the trash this long.
		"assets": gin.H{"trash_retention_days": upkeep.TrashRetentionDays},
		"characters": gin.H{"max_references": capability.CharacterMaxRefImages, "name_max_chars": characterNameMax, "description_max_chars": characterDescriptionMax},
		// Direct uploads: the size each asset type may have once stored.
		"uploads": gin.H{
			"image": gin.H{"max_bytes": maxUploadBytesByType["image"]},
			"video": gin.H{"max_bytes": maxUploadBytesByType["video"]},
			"audio": gin.H{"max_bytes": maxUploadBytesByType["audio"]},
		},
		"image": gin.H{
			"max_n":              capability.ImageMaxN,
			"sequence_max_shots": capability.ImageSequenceMaxShots,
			"max_prompt_chars":   capability.ImageMaxPromptChars,
		},
		"video": gin.H{
			"duration_min":     capability.VideoDurationMin,
			"duration_max":     capability.VideoDurationMax,
			"max_prompt_chars": capability.VideoMaxPromptChars,
			"resolutions":      capability.VideoResolutions,
			"ratios":           capability.VideoRatios,
			// r2va reference videos: a count and a combined-length budget.
			"max_reference_videos":        workflows.MaxReferenceVideoClips,
			"reference_video_max_seconds": capability.ReferenceVideoMaxSeconds,
			"sequence_max_shots":          capability.VideoSequenceMaxShots,
			"max_reference_images":        capability.VideoMaxReferenceImages,
			"max_reference_audios":        capability.VideoMaxReferenceAudios,
		},
		// Which providers can run here; access to OpenAI is additionally
		// gated per user (GET /me entitlements).
		"providers": gin.H{
			"minimax": gin.H{"enabled": config.MiniMaxAPIKey() != "", "video_model": "MiniMax-H3", "image_model": "image-01"},
			"openai": gin.H{
				"enabled": openAI, "image_model": config.OpenAIImageModel(), "entitlement": "openai_image",
				"sizes": config.OpenAIImageSizes(), "qualities": config.OpenAIImageQualities(),
				"max_n": min(config.OpenAIImageMaxN(), capability.ImageMaxN), "default_quality": openai.DefaultQuality,
				// Counted across attached images, character references and
				// sequence links; each file is checked against the formats and size.
				"max_references": openai.MaxReferences, "reference_formats": comic.ReferenceMimes,
				"max_reference_bytes": openai.MaxReferenceBytes,
				"workflows":           []string{"image.single", "image.sequence", "image.comic4"},
			},
			"gemini": gin.H{"enabled": config.GeminiVertexProjectID() != "", "image_model": config.GeminiImageModel()},
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
