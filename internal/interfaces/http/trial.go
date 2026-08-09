package httpapi

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"aigc-platform/internal/infra/executor/minimax"
)

const (
	trialDeviceTTL = 365 * 24 * time.Hour // effectively "once ever" per device fingerprint
	trialIPWindow  = 24 * time.Hour
	trialIPMax     = 3 // generous ceiling against trivial device-fingerprint clearing; device_id is the primary gate
)

// handleTrialImage is F1.2: one anonymous single-image generation, gated by
// a client-generated device fingerprint plus IP rate limiting, backing the
// PRD's "未登录可浏览...匿名试用 1 次｜先给价值再要注册" landing hook.
// Deliberately bypasses the whole jobs/credits/assets pipeline: there's no
// user account yet to own a job or hold credits against, and the PRD frames
// this as a disposable taste rather than something meant to land in a
// permanent library (register to keep it). The generated image's temporary
// MiniMax URL (valid ~24h, PRD R3) is returned directly instead of being
// materialized into our own storage — this is the one deliberate exception
// to "cmd/api never talks to MiniMax directly" (see main.go): an anonymous,
// job-free, credit-free request doesn't fit the authenticated pipeline at
// all, so routing it through cmd/scheduler/worker would need substantially
// more plumbing (an ownerless job, a system user hack) for no benefit.
func (s *Server) handleTrialImage(c *gin.Context) {
	var req struct {
		Prompt   string `json:"prompt"`
		DeviceID string `json:"device_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Prompt == "" || req.DeviceID == "" {
		c.JSON(http.StatusBadRequest, errBody("bad_request", "prompt and device_id required"))
		return
	}
	if r := []rune(req.Prompt); len(r) > 1500 {
		req.Prompt = string(r[:1500])
	}

	ctx := c.Request.Context()
	ipKey := "trial:ip:" + c.ClientIP()
	count, err := s.redis.Incr(ctx, ipKey).Result()
	if err != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "rate limit check failed"))
		return
	}
	if count == 1 {
		s.redis.Expire(ctx, ipKey, trialIPWindow)
	}
	if count > trialIPMax {
		c.JSON(http.StatusTooManyRequests, errBody("rate_limited", "今天试用次数已用完，请注册后继续生成"))
		return
	}

	deviceKey := "trial:device:" + req.DeviceID
	acquired, err := s.redis.SetNX(ctx, deviceKey, "1", trialDeviceTTL).Result()
	if err != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "rate limit check failed"))
		return
	}
	if !acquired {
		c.JSON(http.StatusForbidden, errBody("trial_used", "此设备已使用过匿名试用，请注册后继续生成"))
		return
	}

	resp, err := s.minimax.GenerateImage(ctx, minimax.ImageGenerationRequest{
		Model: "image-01", Prompt: req.Prompt, N: 1, AspectRatio: "1:1", ResponseFormat: "url",
	})
	// A failed call (network, business rejection, or empty result) shouldn't
	// burn the visitor's one trial — undo the device claim before returning.
	if err != nil {
		s.redis.Del(ctx, deviceKey)
		c.JSON(http.StatusBadGateway, errBody("upstream_error", "生成失败，请重试"))
		return
	}
	// Never surface resp.BaseResp.StatusMsg directly — it's MiniMax's own raw
	// (English) upstream text, not something an anonymous visitor should see
	// verbatim. §10.4's 1026 (sensitive content) is the one case worth a
	// specific, actionable message; everything else collapses to a generic
	// one, same as the network/transport branch above.
	if resp.BaseResp.StatusCode != 0 {
		s.redis.Del(ctx, deviceKey)
		msg := "生成失败，请重试"
		if resp.BaseResp.StatusCode == 1026 {
			msg = "描述涉及敏感内容，请修改后重试"
		}
		c.JSON(http.StatusUnprocessableEntity, errBody("generation_failed", msg))
		return
	}
	if len(resp.Data.ImageURLs) == 0 {
		s.redis.Del(ctx, deviceKey)
		c.JSON(http.StatusUnprocessableEntity, errBody("generation_failed", "生成失败，请重试"))
		return
	}

	c.JSON(http.StatusOK, gin.H{"image_url": resp.Data.ImageURLs[0]})
}
