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

	"aigc-platform/internal/domain/capability"
	"aigc-platform/internal/domain/prompt"
	"aigc-platform/internal/infra/executor/assetstore"
	"aigc-platform/internal/infra/executor/spi/executor"
	"aigc-platform/internal/infra/executor/spi/model"
)

// PRD §12.1/§10.5's video pricing table. §10.5 also lists a discounted
// "RegenCostPerSecondYuan: 0.30" for 768P->2K upscaling — not used: MiniMax
// has no upscale primitive, so getting a 2K version is a fresh 2K
// generation at the full 2K rate (see video_regen.go's VideoRegenConfig doc).
var costPerSecondYuan = map[string]float64{"768P": 0.50, "2K": 0.80}

const (
	videoModel        = "MiniMax-H3"
	videoPollInterval = 10 * time.Second // MiniMax's suggested polling cadence
)

// videoBase is the submit/poll/materialize pipeline shared by minimax.video
// and minimax.video.regen; they differ only in content and resolution.
type videoBase struct {
	client      *Client
	sink        assetstore.Sink
	reader      assetstore.Reader
	cache       FileCache
	callbackURL string        // set only when MiniMax can reach this deployment
	limiter     *VideoLimiter // nil means unbounded
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

// VideoPlugin is minimax.video. The orchestrator submits the remote task
// and polls it without holding a worker; Execute runs both in one call for
// direct use.
type VideoPlugin struct {
	base *videoBase
}

func NewVideoPlugin(client *Client, sink assetstore.Sink, reader assetstore.Reader, cache FileCache, callbackURL string, limiter *VideoLimiter) *VideoPlugin {
	return &VideoPlugin{base: &videoBase{client: client, sink: sink, reader: reader, cache: cache, callbackURL: callbackURL, limiter: limiter}}
}

func (p *VideoPlugin) Type() string { return "minimax.video" }

func (p *VideoPlugin) Schema() model.ExecutorSchema {
	return executor.SchemaOf[VideoConfig, executor.DynamicOutputs](
		"minimax.video", "1.0", "MiniMax-H3 asynchronous video generation: t2va/i2va/r2va",
	)
}

func (p *VideoPlugin) params(ctx context.Context, req *executor.ExecuteRequest) (submitParams, *model.ExecOutputs, error) {
	var cfg VideoConfig
	if err := executor.BindInputs(req.Inputs, &cfg); err != nil {
		return submitParams{}, nil, fmt.Errorf("bind minimax.video inputs: %w", err)
	}
	resolution := normalizeResolution(cfg.Resolution)
	params := submitParams{
		Resolution: resolution, Duration: normalizeDuration(cfg.Duration), Prompt: cfg.Prompt, UserID: cfg.UserID,
		AigcWatermark: cfg.AigcWatermark, CostPerSecond: costPerSecondYuan[resolution], ShotIndex: cfg.ShotIndex,
		refs: videoRefs{
			Prompt: cfg.Prompt, Ratio: cfg.Ratio, FirstFrameAssetID: cfg.FirstFrameAssetID, LastFrameAssetID: cfg.LastFrameAssetID,
			ReferenceImageAssetIDs: cfg.ReferenceImageAssetIDs, ReferenceVideoAssetIDs: cfg.ReferenceVideoAssetIDs, ReferenceAudioAssetIDs: cfg.ReferenceAudioAssetIDs,
		},
	}
	return params, nil, nil
}

func (p *VideoPlugin) Submit(ctx context.Context, req *executor.ExecuteRequest) (executor.ProviderRef, *model.ExecOutputs, error) {
	params, _, err := p.params(ctx, req)
	if err != nil {
		return executor.ProviderRef{}, nil, err
	}
	return p.base.submit(ctx, req, params)
}

func (p *VideoPlugin) Poll(ctx context.Context, req *executor.ExecuteRequest, ref executor.ProviderRef) (executor.PollResult, error) {
	params, _, err := p.params(ctx, req)
	if err != nil {
		return executor.PollResult{}, err
	}
	return p.base.poll(ctx, req, ref, params)
}

func (p *VideoPlugin) Execute(ctx context.Context, req *executor.ExecuteRequest) (*model.ExecOutputs, error) {
	return runToCompletion(ctx, p, req)
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

type submitParams struct {
	refs          videoRefs
	Resolution    string
	Duration      int
	Prompt        string
	UserID        string
	AigcWatermark *bool
	CostPerSecond float64
	// RegenOf is the preview asset a 2K upgrade replaces, kept in the new
	// asset's metadata.
	RegenOf   string
	ShotIndex string
}

// submit starts the remote task. The limiter slot is held until the task
// finishes, since MiniMax counts running tasks against the account quota.
func (b *videoBase) submit(ctx context.Context, req *executor.ExecuteRequest, p submitParams) (executor.ProviderRef, *model.ExecOutputs, error) {
	content, _, ratio, errOut := b.buildContent(ctx, p.refs)
	if errOut != nil {
		return executor.ProviderRef{}, errOut, nil
	}
	if b.limiter != nil {
		ok, err := b.limiter.Hold(ctx, req.TaskRunID)
		if err != nil {
			return executor.ProviderRef{}, nil, fmt.Errorf("rate limiter: %w", err)
		}
		if !ok {
			return executor.ProviderRef{}, nil, &executor.NoCapacityError{Reason: "provider_capacity", RetryAfter: 15 * time.Second}
		}
	}
	mmReq := VideoGenerationRequest{Model: videoModel, Content: content, Resolution: p.Resolution, Duration: p.Duration, Ratio: ratio, CallbackURL: b.callbackURL}
	if p.AigcWatermark != nil {
		mmReq.AigcWatermark = *p.AigcWatermark
	}
	created, err := b.client.CreateVideoTask(ctx, mmReq)
	if err != nil {
		b.release(req.TaskRunID)
		var httpErr *HTTPStatusError
		if errors.As(err, &httpErr) && httpErr.StatusCode == 429 {
			return executor.ProviderRef{}, nil, &executor.NoCapacityError{Reason: "provider_rate_limited", RetryAfter: 30 * time.Second}
		}
		return executor.ProviderRef{}, classifyVideoError(err), nil
	}
	if created.TaskID == "" {
		b.release(req.TaskRunID)
		return executor.ProviderRef{}, errOutputs(model.ExecCodeError, fmt.Sprintf("no task id (status %d: %s)", created.BaseResp.StatusCode, created.BaseResp.StatusMsg)), nil
	}
	return executor.ProviderRef{Provider: "minimax", TaskID: created.TaskID, SubmittedAt: time.Now().UTC()}, nil, nil
}

func (b *videoBase) release(token string) {
	if b.limiter != nil {
		b.limiter.Release(context.Background(), token)
	}
}

// poll checks the task and, once it succeeded, downloads and stores the clip
// immediately: MiniMax's result URL is temporary.
func (b *videoBase) poll(ctx context.Context, req *executor.ExecuteRequest, ref executor.ProviderRef, p submitParams) (executor.PollResult, error) {
	task, err := b.client.QueryVideoTask(ctx, ref.TaskID)
	if err != nil {
		return executor.PollResult{}, err
	}
	switch task.Task.Status {
	case "succeeded":
	case "failed":
		b.release(req.TaskRunID)
		msg := "failed"
		if task.Task.Error != nil {
			msg = task.Task.Error.Code + ": " + task.Task.Error.Message
		}
		return executor.PollResult{Done: true, Outputs: errOutputs(model.ExecCodeFailed, msg)}, nil
	case "cancelled":
		b.release(req.TaskRunID)
		return executor.PollResult{Done: true, Outputs: errOutputs(model.ExecCodeFailed, "cancelled_upstream")}, nil
	default:
		if b.limiter != nil {
			_, _ = b.limiter.Hold(ctx, req.TaskRunID)
		}
		return executor.PollResult{After: videoPollInterval}, nil
	}
	if task.Task.Content == nil || task.Task.Content.URL == "" {
		b.release(req.TaskRunID)
		return executor.PollResult{Done: true, Outputs: errOutputs(model.ExecCodeError, "succeeded task has no content.url")}, nil
	}
	data, err := b.client.DownloadVideo(ctx, task.Task.Content.URL)
	if err != nil {
		return executor.PollResult{}, fmt.Errorf("download video: %w", err)
	}
	outputSeconds := p.Duration
	if task.Task.Usage != nil && task.Task.Usage.OutputSeconds > 0 {
		outputSeconds = task.Task.Usage.OutputSeconds
	}
	resolutionTag := task.Task.Resolution
	if resolutionTag == "" {
		resolutionTag = p.Resolution
	}
	_, mode, _, _ := b.modeOnly(p.refs)
	meta := map[string]any{"model": videoModel, "mode": mode, "prompt": p.Prompt, "minimax_task_id": ref.TaskID}
	if p.RegenOf != "" {
		meta["regen_of_asset_id"] = p.RegenOf
	}
	width, height := probeVideoDimensions(ctx, data)
	assetID, err := b.sink.MaterializeBytes(ctx, assetstore.NewAssetBytes{
		UserID: parseUserID(p.UserID), Type: "video", Source: "generated", FromTaskRunID: req.TaskRunID,
		Body: bytesReader(data), SizeBytes: int64(len(data)), Ext: "mp4", Mime: "video/mp4",
		Width: width, Height: height, DurationMs: outputSeconds * 1000, ResolutionTag: resolutionTag, Meta: meta,
	})
	if err != nil {
		return executor.PollResult{}, fmt.Errorf("materialize video: %w", err)
	}
	b.release(req.TaskRunID)
	out, err := executor.OutputFrom(struct {
		AssetID       string   `json:"asset-id"`
		AssetIDs      []string `json:"asset-ids"`
		OutputSeconds int      `json:"output-seconds"`
		Resolution    string   `json:"resolution"`
		CostYuan      float64  `json:"cost-yuan"`
		MinimaxTaskID string   `json:"minimax-task-id"`
		ShotIndex     string   `json:"shot-index,omitempty"`
	}{
		AssetID: assetID, AssetIDs: []string{assetID}, OutputSeconds: outputSeconds, Resolution: resolutionTag,
		CostYuan: float64(outputSeconds) * p.CostPerSecond, MinimaxTaskID: ref.TaskID, ShotIndex: p.ShotIndex,
	})
	if err != nil {
		return executor.PollResult{}, err
	}
	return executor.PollResult{Done: true, Outputs: out}, nil
}

// modeOnly reports the generation mode for metadata without resolving refs.
func (b *videoBase) modeOnly(r videoRefs) ([]VideoContentItem, string, string, error) {
	mode, ratio, _, err := prompt.CompileVideoRefs(prompt.VideoRefs{
		Ratio: r.Ratio, FirstFrameAssetID: r.FirstFrameAssetID, LastFrameAssetID: r.LastFrameAssetID,
		ReferenceImageAssetIDs: r.ReferenceImageAssetIDs, ReferenceVideoAssetIDs: r.ReferenceVideoAssetIDs, ReferenceAudioAssetIDs: r.ReferenceAudioAssetIDs,
	})
	return nil, mode, ratio, err
}

// runToCompletion drives an async plugin synchronously for callers outside
// the orchestrator. Exhausted capacity is reported as a retryable error.
func runToCompletion(ctx context.Context, a executor.AsyncPlugin, req *executor.ExecuteRequest) (*model.ExecOutputs, error) {
	ref, out, err := a.Submit(ctx, req)
	if nc, ok := executor.AsNoCapacity(err); ok {
		return errOutputs(model.ExecCodeError, "no_capacity: "+nc.Reason), nil
	}
	if out != nil || err != nil {
		return out, err
	}
	for {
		res, err := a.Poll(ctx, req, ref)
		if err == nil && res.Done {
			return res.Outputs, nil
		}
		wait := res.After
		if wait <= 0 {
			wait = videoPollInterval
		}
		select {
		case <-ctx.Done():
			return errOutputs(model.ExecCodeTimeout, "wait_timeout: "+ctx.Err().Error()), nil
		case <-time.After(wait):
		}
	}
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

var _ executor.AsyncPlugin = (*VideoPlugin)(nil)
