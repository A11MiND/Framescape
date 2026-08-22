package minimax

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/BabySid/aether/executor"
	"github.com/BabySid/aether/model"
	"github.com/redis/go-redis/v9"

	"aigc-platform/internal/domain/capability"
	"aigc-platform/internal/domain/prompt"
	"aigc-platform/internal/infra/executor/assetstore"
)

// PRD §12.1/§10.5's video pricing table. §10.5 also lists a discounted
// "RegenCostPerSecondYuan: 0.30" for 768P->2K upscaling — not used: MiniMax
// has no upscale primitive, so getting a 2K version is a fresh 2K
// generation at the full 2K rate (see video_regen.go's VideoRegenConfig doc).
var costPerSecondYuan = map[string]float64{"768P": 0.50, "2K": 0.80}

const (
	videoModel        = "MiniMax-H3"
	videoPollInterval = 10 * time.Second // MiniMax's suggested polling cadence (§3.2/§11.3)
	videoMaxWait      = 25 * time.Minute // deliberately under Aether's 30m task timeout (§10.3)
)

// videoBase holds the dependencies and submit/wait/materialize pipeline
// shared by minimax.video and minimax.video.regen (video.go / video_regen.go)
// — both submit to the same /v2/video_generation endpoint and only differ in
// how their `content` array and target resolution get built.
type videoBase struct {
	client      *Client
	sink        assetstore.Sink
	reader      assetstore.Reader
	cache       FileCache
	redis       *redis.Client // optional: nil disables callback fast-path, falls back to pure polling
	callbackURL string        // optional: only set on the request if this deployment has a public endpoint MiniMax can reach
	limiter     *VideoLimiter // optional: nil means unbounded, see VideoLimiter.Acquire
}

// VideoConfig is minimax.video's declared input contract (Aether kebab-case
// protocol naming — see client.go's package doc). Exactly one of
// {FirstFrameAssetID, LastFrameAssetID} XOR {ReferenceImageAssetIDs,
// ReferenceVideoAssetIDs, ReferenceAudioAssetIDs} may be set (§3.2's
// mode mutual-exclusion, enforced in Execute as a backend backstop —
// the frontend's F6.5 is the primary guard, §19.4.1).
type VideoConfig struct {
	Prompt     string `json:"prompt"`
	Duration   string `json:"duration"`   // 4..15 seconds, string for the same reason as ImageConfig.N
	Resolution string `json:"resolution"` // 768P | 2K, default 768P
	Ratio      string `json:"ratio"`      // required+non-adaptive for t2va; forced adaptive for i2va; optional (default adaptive) for r2va

	FirstFrameAssetID string `json:"first-frame-asset-id"`
	LastFrameAssetID  string `json:"last-frame-asset-id"`

	ReferenceImageAssetIDs []string `json:"reference-image-asset-ids"`
	ReferenceVideoAssetIDs []string `json:"reference-video-asset-ids"`
	ReferenceAudioAssetIDs []string `json:"reference-audio-asset-ids"`

	AigcWatermark *bool  `json:"aigc-watermark"`
	UserID        string `json:"user-id"`
	// ShotIndex is optional and meaningless to this executor itself — it
	// exists purely so a Loop body invocation (video.sequence's redo path,
	// W6) can echo it back as an output, letting the Loop's `aggregate`
	// collect a parallel shot-index[] array alongside asset-id[] so the
	// concat step can re-order results by shot after the loop's own
	// iteration order (not necessarily 1..N) completes.
	ShotIndex string `json:"shot-index,omitempty"`
}

// VideoPlugin is minimax.video (PRD §10.3, option A: Execute blocks until a
// terminal task state — the Worker pool for q:video is sized to MiniMax's
// concurrency quota, §11.1, so a blocked slot isn't wasted capacity).
type VideoPlugin struct {
	base *videoBase
}

// NewVideoPlugin's redis/callbackURL are both optional (pass nil/"" to run
// polling-only, the only mode exercisable from a dev machine behind NAT —
// MiniMax cannot reach a callback URL that isn't publicly routable). limiter
// may also be nil (unbounded) — see VideoLimiter.Acquire.
func NewVideoPlugin(client *Client, sink assetstore.Sink, reader assetstore.Reader, cache FileCache, redisClient *redis.Client, callbackURL string, limiter *VideoLimiter) *VideoPlugin {
	return &VideoPlugin{base: &videoBase{
		client: client, sink: sink, reader: reader, cache: cache,
		redis: redisClient, callbackURL: callbackURL, limiter: limiter,
	}}
}

func (p *VideoPlugin) Type() string { return "minimax.video" }

func (p *VideoPlugin) Schema() model.ExecutorSchema {
	return executor.SchemaOf[VideoConfig, executor.DynamicOutputs](
		"minimax.video", "1.0", "MiniMax-H3 asynchronous video generation: t2va/i2va/r2va (PRD §3.2/§10.3)",
	)
}

func (p *VideoPlugin) Execute(ctx context.Context, req *executor.ExecuteRequest) (*model.ExecOutputs, error) {
	var cfg VideoConfig
	if err := executor.BindInputs(req.Inputs, &cfg); err != nil {
		return nil, fmt.Errorf("bind minimax.video inputs: %w", err)
	}

	duration := normalizeDuration(cfg.Duration)
	cfg.Resolution = normalizeResolution(cfg.Resolution)

	content, mode, ratio, errOut := p.base.buildContent(ctx, videoRefs{
		Prompt:                 cfg.Prompt,
		Ratio:                  cfg.Ratio,
		FirstFrameAssetID:      cfg.FirstFrameAssetID,
		LastFrameAssetID:       cfg.LastFrameAssetID,
		ReferenceImageAssetIDs: cfg.ReferenceImageAssetIDs,
		ReferenceVideoAssetIDs: cfg.ReferenceVideoAssetIDs,
		ReferenceAudioAssetIDs: cfg.ReferenceAudioAssetIDs,
	})
	if errOut != nil {
		return errOut, nil
	}

	return p.base.submitWaitMaterialize(ctx, req, submitParams{
		Content:       content,
		Resolution:    cfg.Resolution,
		Duration:      duration,
		Ratio:         ratio,
		Mode:          mode,
		Prompt:        cfg.Prompt,
		UserID:        cfg.UserID,
		AigcWatermark: cfg.AigcWatermark,
		CostPerSecond: costPerSecondYuan[cfg.Resolution],
		ShotIndex:     cfg.ShotIndex,
	})
}

// videoRefs is buildContent's input: everything needed to assemble one
// video_generation request's `content` array, independent of whether the
// caller is minimax.video or minimax.video.regen.
type videoRefs struct {
	Prompt                 string
	Ratio                  string
	FirstFrameAssetID      string
	LastFrameAssetID       string
	ReferenceImageAssetIDs []string
	ReferenceVideoAssetIDs []string
	ReferenceAudioAssetIDs []string
}

// itemTypeForKind maps prompt.VideoRefItem.Kind to VideoContentItem's own
// `type` enum — the one piece of provider-wire-format knowledge buildContent
// still owns, since prompt.CompileVideoRefs stays free of MiniMax's own
// naming (client.go's package doc: two different wire vocabularies).
func itemTypeForKind(kind string) string {
	switch kind {
	case "video":
		return "video_url"
	case "audio":
		return "audio_url"
	default:
		return "image_url"
	}
}

// buildContent resolves prompt.CompileVideoRefs' domain-level decision (mode,
// ratio, ordered ref list — PRD §5.3 steps 3/4, moved into the prompt
// package so this executor isn't the one deciding it — see that function's
// own doc) into a real video_generation `content` array: each ref becomes a
// live mm_file:// reference via refItem, which is genuinely provider-
// specific I/O and stays here. Shared by minimax.video and
// minimax.video.regen — regen submits to the same endpoint with the same
// content, just resolution forced to 2K (see video_regen.go's
// VideoRegenConfig doc for why there's no other difference).
func (b *videoBase) buildContent(ctx context.Context, r videoRefs) (content []VideoContentItem, mode string, ratio string, errOut *model.ExecOutputs) {
	promptText := prompt.TruncateVideoPrompt(r.Prompt)

	mode, ratio, refItems, err := prompt.CompileVideoRefs(prompt.VideoRefs{
		Ratio:                  r.Ratio,
		FirstFrameAssetID:      r.FirstFrameAssetID,
		LastFrameAssetID:       r.LastFrameAssetID,
		ReferenceImageAssetIDs: r.ReferenceImageAssetIDs,
		ReferenceVideoAssetIDs: r.ReferenceVideoAssetIDs,
		ReferenceAudioAssetIDs: r.ReferenceAudioAssetIDs,
	})
	if err != nil {
		// prompt.CompileVideoRefs' one validation failure (mutual exclusion)
		// and its bad_params ratio check are both business-rule rejections,
		// not system errors — ExecCodeFailed either way, same as before
		// this moved out of this function's own inline checks.
		return nil, "", "", errOutputs(model.ExecCodeFailed, err.Error())
	}

	content = []VideoContentItem{{Type: "text", Text: promptText}}
	for _, item := range refItems {
		resolved, err := b.refItem(ctx, item.AssetBizID, itemTypeForKind(item.Kind), item.Role)
		if err != nil {
			return nil, "", "", errOutputs(model.ExecCodeError, err.Error())
		}
		content = append(content, resolved)
	}
	return content, mode, ratio, nil
}

// submitParams is everything submitWaitMaterialize needs beyond content
// itself — kept as one struct so both callers (VideoPlugin, VideoRegenPlugin)
// pass a single value instead of a long positional argument list.
type submitParams struct {
	Content       []VideoContentItem
	Resolution    string
	Duration      int
	Ratio         string
	Mode          string
	Prompt        string
	UserID        string
	AigcWatermark *bool
	CostPerSecond float64
	// RegenOf is set only by VideoRegenPlugin — the 768P source asset being
	// upscaled, recorded in the resulting 2K asset's Meta for traceability.
	RegenOf string
	// ShotIndex is pass-through only, see VideoConfig.ShotIndex's doc.
	ShotIndex string
}

// submitWaitMaterialize is §10.3's phases two and three (submit already
// happened by the time Content is built; this does submit -> wait ->
// materialize) shared verbatim by minimax.video and minimax.video.regen.
func (b *videoBase) submitWaitMaterialize(ctx context.Context, req *executor.ExecuteRequest, p submitParams) (*model.ExecOutputs, error) {
	mmReq := VideoGenerationRequest{
		Model:      videoModel,
		Content:    p.Content,
		Resolution: p.Resolution,
		Duration:   p.Duration,
		Ratio:      p.Ratio,
	}
	if p.AigcWatermark != nil {
		mmReq.AigcWatermark = *p.AigcWatermark
	}
	if b.callbackURL != "" {
		mmReq.CallbackURL = b.callbackURL
	}

	if b.limiter != nil {
		release, ok, err := b.limiter.Acquire(ctx, req.TaskRunID)
		if err != nil {
			return errOutputs(model.ExecCodeError, "rate_limiter: "+err.Error()), nil
		}
		if !ok {
			// Not a business failure — §11.2's "拿不到令牌返回 ExecCodeError,
			// 引擎按退避策略重试". The slot is only worth holding for the
			// submit+wait+download below, not the validation done before it.
			return errOutputs(model.ExecCodeError, "no_capacity: video concurrency limit reached"), nil
		}
		defer release()
	}

	created, err := b.client.CreateVideoTask(ctx, mmReq)
	if err != nil {
		return classifyVideoError(err), nil
	}
	taskID := created.TaskID

	task, waitErr := b.wait(ctx, taskID)
	if waitErr != nil {
		return errOutputs(model.ExecCodeTimeout, "wait_timeout: "+waitErr.Error()), nil
	}

	switch task.Task.Status {
	case "failed":
		msg := "failed"
		if task.Task.Error != nil {
			msg = task.Task.Error.Code + ": " + task.Task.Error.Message
		}
		return errOutputs(model.ExecCodeFailed, msg), nil
	case "cancelled":
		return errOutputs(model.ExecCodeFailed, "cancelled_upstream"), nil
	case "succeeded":
		// continue below
	default:
		return errOutputs(model.ExecCodeError, "unexpected terminal status: "+task.Task.Status), nil
	}
	if task.Task.Content == nil || task.Task.Content.URL == "" {
		return errOutputs(model.ExecCodeError, "succeeded task has no content.url"), nil
	}

	// §R3: materialize immediately — MiniMax's URL is temporary.
	data, err := b.client.DownloadVideo(ctx, task.Task.Content.URL)
	if err != nil {
		return errOutputs(model.ExecCodeError, "download video: "+err.Error()), nil
	}

	outputSeconds := p.Duration
	if task.Task.Usage != nil && task.Task.Usage.OutputSeconds > 0 {
		outputSeconds = task.Task.Usage.OutputSeconds
	}
	resolutionTag := task.Task.Resolution
	if resolutionTag == "" {
		resolutionTag = p.Resolution
	}

	meta := map[string]any{
		"model":           videoModel,
		"mode":            p.Mode,
		"prompt":          p.Prompt,
		"minimax_task_id": taskID,
	}
	if p.RegenOf != "" {
		meta["regen_of_asset_id"] = p.RegenOf
	}

	// width/height were never populated here — unlike local.ffmpeg's
	// extract/concat outputs, which run ffprobe on their own products, the
	// raw video.single/video.sequence output straight off MiniMax's URL had
	// no dimension probe of its own, so every such asset row had width=0
	// height=0 (DEV_PLAN.md's long-recorded gap). Best-effort: a missing
	// ffprobe binary or a decode failure degrades to 0/0 exactly as before,
	// it never fails the job over a cosmetic field.
	width, height := probeVideoDimensions(ctx, data)

	assetID, err := b.sink.MaterializeBytes(ctx, assetstore.NewAssetBytes{
		UserID:        parseUserID(p.UserID),
		Type:          "video",
		Source:        "generated",
		FromTaskRunID: req.TaskRunID,
		Body:          bytesReader(data),
		SizeBytes:     int64(len(data)),
		Ext:           "mp4",
		Mime:          "video/mp4",
		Width:         width,
		Height:        height,
		DurationMs:    outputSeconds * 1000,
		ResolutionTag: resolutionTag,
		Meta:          meta,
	})
	if err != nil {
		return nil, fmt.Errorf("materialize video: %w", err)
	}

	return executor.OutputFrom(struct {
		AssetID string `json:"asset-id"`
		// AssetIDs (plural) mirrors image.go's convention even though video
		// only ever produces one asset — job_nodes' projection (§6/W2) only
		// reads the plural key when populating its own asset_ids column.
		AssetIDs      []string `json:"asset-ids"`
		OutputSeconds int      `json:"output-seconds"`
		Resolution    string   `json:"resolution"`
		CostYuan      float64  `json:"cost-yuan"`
		MinimaxTaskID string   `json:"minimax-task-id"`
		ShotIndex     string   `json:"shot-index,omitempty"`
	}{
		AssetID:       assetID,
		AssetIDs:      []string{assetID},
		OutputSeconds: outputSeconds,
		Resolution:    resolutionTag,
		CostYuan:      float64(outputSeconds) * p.CostPerSecond,
		MinimaxTaskID: taskID,
		ShotIndex:     p.ShotIndex,
	})
}

// refItem resolves a local asset to a MiniMax mm_file:// reference, uploading
// it (or reusing the provider_files cache) since our object storage is not
// internet-reachable — MiniMax's servers can never fetch a raw URL pointing
// back at our own MinIO (docs/aether-validation-report.md's W5 addendum).
func (b *videoBase) refItem(ctx context.Context, assetBizID, itemType, role string) (VideoContentItem, error) {
	fileID, _, err := uploadOrGetCached(ctx, b.client, b.reader, b.cache, assetBizID, defaultUploadPurpose)
	if err != nil {
		return VideoContentItem{}, fmt.Errorf("resolve %s asset %s: %w", role, assetBizID, err)
	}
	ref := &URLRef{URL: "mm_file://" + fileID}
	item := VideoContentItem{Type: itemType, Role: role}
	switch itemType {
	case "image_url":
		item.ImageURL = ref
	case "video_url":
		item.VideoURL = ref
	case "audio_url":
		item.AudioURL = ref
	}
	return item, nil
}

// wait implements §11.3's "callback-first, polling-fallback" strategy: if a
// redis client is wired, it subscribes to the channel the callback handler
// publishes to (see internal/interfaces/http's callback route) as a
// fast-path wakeup, but always still polls on videoPollInterval regardless —
// a missed/undeliverable callback (this dev environment has no public URL at
// all) must never stall the wait.
func (b *videoBase) wait(ctx context.Context, taskID string) (*VideoTaskStatus, error) {
	deadline := time.Now().Add(videoMaxWait)

	var wake <-chan *redis.Message
	if b.redis != nil {
		sub := b.redis.Subscribe(ctx, videoCallbackChannel(taskID))
		defer sub.Close()
		wake = sub.Channel()
	}

	ticker := time.NewTicker(videoPollInterval)
	defer ticker.Stop()

	for {
		// A single flaky poll (network blip, upstream 5xx) must not abort a
		// 25-minute wait — only ctx cancellation or the deadline itself ends
		// it early; any other query error just gets retried on the next tick.
		if status, err := b.client.QueryVideoTask(ctx, taskID); err == nil && isTerminalVideoStatus(status.Task.Status) {
			return status, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("exceeded %s", videoMaxWait)
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-wake:
		case <-ticker.C:
		}
	}
}

func normalizeDuration(s string) int {
	duration, _ := strconv.Atoi(s)
	if duration <= 0 {
		duration = 5
	}
	if duration < capability.VideoDurationMin {
		duration = capability.VideoDurationMin
	}
	if duration > capability.VideoDurationMax {
		duration = capability.VideoDurationMax // MiniMax hard limit, §3.2
	}
	return duration
}

// normalizeResolution defends costPerSecondYuan's lookup: jobsvc.Create
// already rejects any resolution outside {"", "768P", "2K"} at the API
// boundary (its own backstop, mirroring this file's mode mutual-exclusion
// check), but this is the last line of defense inside the executor itself —
// without it, an unrecognized string would miss the costPerSecondYuan map
// and silently price the video at ¥0/s (a real free-generation bug, not
// hypothetical: CostYuan below is computed straight from this lookup).
func normalizeResolution(r string) string {
	if r == "2K" {
		return "2K"
	}
	return "768P"
}

func isTerminalVideoStatus(status string) bool {
	switch status {
	case "succeeded", "failed", "cancelled":
		return true
	default:
		return false
	}
}

// videoCallbackChannel is the Redis Pub/Sub channel the callback HTTP
// handler publishes to on every status push — shared naming contract with
// internal/interfaces/http's callback route.
func videoCallbackChannel(taskID string) string { return "minimax:video:" + taskID }

// classifyVideoError implements §10.4's video column: real HTTP status codes
// (unlike image_generation's body-embedded base_resp.status_code). A plain
// transport failure (no HTTP response at all — DNS, connection refused,
// timeout) is always retryable, same as the network-timeout row.
func classifyVideoError(err error) *model.ExecOutputs {
	var httpErr *HTTPStatusError
	if !errors.As(err, &httpErr) {
		return errOutputs(model.ExecCodeError, "transport: "+err.Error())
	}
	switch httpErr.StatusCode {
	case 429:
		return errOutputs(model.ExecCodeError, "rate_limited: "+httpErr.Body)
	case 401:
		return errOutputs(model.ExecCodeFailed, "auth_error: "+httpErr.Body)
	case 402:
		return errOutputs(model.ExecCodeFailed, "insufficient_balance: "+httpErr.Body)
	case 422:
		return errOutputs(model.ExecCodeFailed, "sensitive_content: "+httpErr.Body)
	case 400:
		return errOutputs(model.ExecCodeFailed, "bad_params: "+httpErr.Body)
	case 500, 529:
		return errOutputs(model.ExecCodeError, fmt.Sprintf("upstream(%d): %s", httpErr.StatusCode, httpErr.Body))
	default:
		return errOutputs(model.ExecCodeError, fmt.Sprintf("unclassified(%d): %s", httpErr.StatusCode, httpErr.Body))
	}
}

// probeVideoDimensions shells out to ffprobe against a temp file — MiniMax's
// API response never reports pixel dimensions (only the resolution *tag*,
// "768P"/"2K"), so the only ground truth is the file itself. ffprobe needs a
// real path, not a byte slice, hence the temp file.
func probeVideoDimensions(ctx context.Context, data []byte) (width, height int) {
	tmp, err := os.CreateTemp("", "aigc-video-dim-*.mp4")
	if err != nil {
		return 0, 0
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if _, err := tmp.Write(data); err != nil {
		return 0, 0
	}
	if err := tmp.Close(); err != nil {
		return 0, 0
	}

	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, "ffprobe", "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=width,height", "-of", "csv=s=x:p=0", tmp.Name())
	out, err := cmd.Output()
	if err != nil {
		return 0, 0
	}
	parts := strings.Split(strings.TrimSpace(string(out)), "x")
	if len(parts) != 2 {
		return 0, 0
	}
	w, err1 := strconv.Atoi(parts[0])
	h, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 0, 0
	}
	return w, h
}

var _ executor.Plugin = (*VideoPlugin)(nil)
