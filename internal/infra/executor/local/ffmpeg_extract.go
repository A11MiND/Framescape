package local

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/jpeg" // decoder registration for image.DecodeConfig
	"os"
	"os/exec"
	"time"

	"github.com/BabySid/aether/executor"
	"github.com/BabySid/aether/model"

	"aigc-platform/internal/infra/executor/assetstore"
)

// ExtractFramesConfig names the video asset to pull frames from (F2.6: "视频
// 自动抽首/尾帧存为 image asset" — the basis for video.sequence's tail-frame
// continuity, W6).
type ExtractFramesConfig struct {
	VideoAssetID string `json:"video-asset-id"`
	UserID       string `json:"user-id"`
}

type ExtractFramesPlugin struct {
	reader assetstore.Reader
	sink   assetstore.Sink
}

func NewExtractFramesPlugin(reader assetstore.Reader, sink assetstore.Sink) *ExtractFramesPlugin {
	return &ExtractFramesPlugin{reader: reader, sink: sink}
}

func (p *ExtractFramesPlugin) Type() string { return "local.ffmpeg.extract" }

func (p *ExtractFramesPlugin) Schema() model.ExecutorSchema {
	return executor.SchemaOf[ExtractFramesConfig, executor.DynamicOutputs](
		"local.ffmpeg.extract", "1.0", "Extract first/last frame of a video asset as image assets (F2.6)",
	)
}

func (p *ExtractFramesPlugin) Execute(ctx context.Context, req *executor.ExecuteRequest) (*model.ExecOutputs, error) {
	var cfg ExtractFramesConfig
	if err := executor.BindInputs(req.Inputs, &cfg); err != nil {
		return nil, fmt.Errorf("bind local.ffmpeg.extract inputs: %w", err)
	}

	url, err := p.reader.PublicURL(ctx, cfg.VideoAssetID)
	if err != nil {
		return &model.ExecOutputs{Code: model.ExecCodeError, Message: fmt.Sprintf("look up video asset %s: %v", cfg.VideoAssetID, err)}, nil
	}

	duration, err := ffprobeDuration(ctx, url)
	if err != nil {
		return &model.ExecOutputs{Code: model.ExecCodeError, Message: fmt.Sprintf("ffprobe duration: %v", err)}, nil
	}

	firstPath, err := extractFrameAt(ctx, url, 0)
	if err != nil {
		return &model.ExecOutputs{Code: model.ExecCodeError, Message: fmt.Sprintf("extract first frame: %v", err)}, nil
	}
	defer os.Remove(firstPath)

	// A hair before the true end avoids ffmpeg landing exactly on/after the
	// last valid frame and returning nothing.
	lastPos := duration - 0.1
	if lastPos < 0 {
		lastPos = 0
	}
	lastPath, err := extractFrameAt(ctx, url, lastPos)
	if err != nil {
		return &model.ExecOutputs{Code: model.ExecCodeError, Message: fmt.Sprintf("extract last frame: %v", err)}, nil
	}
	defer os.Remove(lastPath)

	firstID, err := p.materializeFrame(ctx, req.TaskRunID, cfg.UserID, firstPath, "first")
	if err != nil {
		return &model.ExecOutputs{Code: model.ExecCodeError, Message: err.Error()}, nil
	}
	lastID, err := p.materializeFrame(ctx, req.TaskRunID, cfg.UserID, lastPath, "last")
	if err != nil {
		return &model.ExecOutputs{Code: model.ExecCodeError, Message: err.Error()}, nil
	}

	return executor.OutputFrom(struct {
		FirstFrameAssetID string `json:"first-frame-asset-id"`
		LastFrameAssetID  string `json:"last-frame-asset-id"`
	}{FirstFrameAssetID: firstID, LastFrameAssetID: lastID})
}

func (p *ExtractFramesPlugin) materializeFrame(ctx context.Context, taskRunID, userID, path, which string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s frame: %w", which, err)
	}
	width, height := frameDimensions(data)
	return p.sink.MaterializeBytes(ctx, assetstore.NewAssetBytes{
		UserID:        parseUserID(userID),
		Type:          "image",
		Source:        "derived",
		FromTaskRunID: taskRunID,
		Body:          bytes.NewReader(data),
		SizeBytes:     int64(len(data)),
		Ext:           "jpg",
		Mime:          "image/jpeg",
		Width:         width,
		Height:        height,
		Meta:          map[string]any{"extracted_from_video": true, "frame": which},
	})
}

func ffprobeDuration(ctx context.Context, url string) (float64, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1", url)
	out, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("ffprobe: %w", err)
	}
	var d float64
	if _, err := fmt.Sscanf(string(out), "%f", &d); err != nil {
		return 0, fmt.Errorf("parse ffprobe duration %q: %w", string(out), err)
	}
	return d, nil
}

func extractFrameAt(ctx context.Context, url string, seconds float64) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := os.CreateTemp("", "frame-*.jpg")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	outPath := out.Name()
	out.Close()

	cmd := exec.CommandContext(ctx, "ffmpeg", "-y", "-ss", fmt.Sprintf("%.3f", seconds), "-i", url,
		"-frames:v", "1", "-q:v", "2", outPath)
	if err := cmd.Run(); err != nil {
		os.Remove(outPath)
		return "", fmt.Errorf("ffmpeg: %w", err)
	}
	return outPath, nil
}

func frameDimensions(data []byte) (width, height int) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}

var _ executor.Plugin = (*ExtractFramesPlugin)(nil)
