// Package local implements executors that don't call any external provider
// (PRD §10.1: local.compose/local.ffmpeg.*/local.moderation). compose.go is
// F5.3's "自动拼版": tiles the four comic-panel images into one 2x2 grid.
package local

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	_ "image/png" // decoder registration (jpeg is imported non-blank below since we also Encode with it)
	"net/http"
	"strconv"
	"time"

	"github.com/BabySid/aether/executor"
	"github.com/BabySid/aether/model"
	"go.uber.org/zap"

	"aigc-platform/internal/infra/executor/assetstore"
	"aigc-platform/internal/pkg/logger"
)

const tileSize = 768 // each panel is resized to a tileSize x tileSize square before tiling

type ComposeConfig struct {
	AssetIDs []string `json:"asset-ids"`
	Layout   string   `json:"layout"` // "2x2" (only option for now)
	UserID   string   `json:"user-id"`
}

type ComposePlugin struct {
	reader assetstore.Reader
	sink   assetstore.Sink
	http   *http.Client
}

func NewComposePlugin(reader assetstore.Reader, sink assetstore.Sink) *ComposePlugin {
	// A client with no timeout hangs forever on a stalled connection — the
	// task-level "timeout" in the workflow JSON is the real backstop
	// (enforced by aetherengine.PollingTimeoutWatcher), but a plugin should
	// never rely solely on the engine noticing; fail fast on its own too.
	return &ComposePlugin{reader: reader, sink: sink, http: &http.Client{Timeout: 30 * time.Second}}
}

func (p *ComposePlugin) Type() string { return "local.compose" }

func (p *ComposePlugin) Schema() model.ExecutorSchema {
	return executor.SchemaOf[ComposeConfig, executor.DynamicOutputs](
		"local.compose", "1.0", "Tile N images into a grid (F5.3 四格漫画自动拼版)",
	)
}

func (p *ComposePlugin) Execute(ctx context.Context, req *executor.ExecuteRequest) (*model.ExecOutputs, error) {
	log := logger.From(ctx)

	var cfg ComposeConfig
	if err := executor.BindInputs(req.Inputs, &cfg); err != nil {
		return nil, fmt.Errorf("bind local.compose inputs: %w", err)
	}
	if len(cfg.AssetIDs) == 0 {
		return &model.ExecOutputs{Code: model.ExecCodeFailed, Message: "no asset-ids to compose"}, nil
	}

	tiles := make([]image.Image, 0, len(cfg.AssetIDs))
	for _, assetID := range cfg.AssetIDs {
		url, err := p.reader.PublicURL(ctx, assetID)
		if err != nil {
			return &model.ExecOutputs{Code: model.ExecCodeError, Message: fmt.Sprintf("look up asset %s: %v", assetID, err)}, nil
		}
		img, err := p.fetchImage(ctx, url)
		if err != nil {
			return &model.ExecOutputs{Code: model.ExecCodeError, Message: fmt.Sprintf("fetch asset %s: %v", assetID, err)}, nil
		}
		tiles = append(tiles, img)
	}

	grid := composeGrid(tiles)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, grid, &jpeg.Options{Quality: 90}); err != nil {
		return nil, fmt.Errorf("encode composed grid: %w", err)
	}

	bizID, err := p.sink.MaterializeBytes(ctx, assetstore.NewAssetBytes{
		UserID:        parseUserID(cfg.UserID),
		Type:          "image",
		Source:        "derived",
		FromTaskRunID: req.TaskRunID,
		Body:          bytes.NewReader(buf.Bytes()),
		SizeBytes:     int64(buf.Len()),
		Ext:           "jpg",
		Mime:          "image/jpeg",
		Width:         grid.Bounds().Dx(),
		Height:        grid.Bounds().Dy(),
		Meta: map[string]any{
			"composed_from": cfg.AssetIDs,
			"layout":        "2x2",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("materialize composed grid: %w", err)
	}
	log.Debug("compose: composed grid ready", zap.String("biz_id", bizID))

	return executor.OutputFrom(struct {
		AssetID string `json:"asset-id"`
	}{AssetID: bizID})
}

func (p *ComposePlugin) fetchImage(ctx context.Context, url string) (image.Image, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	img, _, err := image.Decode(resp.Body)
	return img, err
}

// composeGrid tiles up to 4 images into a fixed 2x2 canvas (F5.3's only
// supported layout). Each source image is scaled to fill a tileSize square
// via nearest-neighbor (fine for POC; a real resampler is a cosmetic
// upgrade, not a correctness one).
func composeGrid(tiles []image.Image) *image.RGBA {
	canvas := image.NewRGBA(image.Rect(0, 0, tileSize*2, tileSize*2))
	positions := []image.Point{{0, 0}, {tileSize, 0}, {0, tileSize}, {tileSize, tileSize}}
	for i, tile := range tiles {
		if i >= 4 {
			break
		}
		scaled := scaleToSquare(tile, tileSize)
		dstRect := image.Rect(positions[i].X, positions[i].Y, positions[i].X+tileSize, positions[i].Y+tileSize)
		draw.Draw(canvas, dstRect, scaled, image.Point{}, draw.Src)
	}
	return canvas
}

func scaleToSquare(src image.Image, size int) image.Image {
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	sb := src.Bounds()
	for y := 0; y < size; y++ {
		sy := sb.Min.Y + y*sb.Dy()/size
		for x := 0; x < size; x++ {
			sx := sb.Min.X + x*sb.Dx()/size
			dst.Set(x, y, src.At(sx, sy))
		}
	}
	return dst
}

func parseUserID(s string) uint64 {
	v, _ := strconv.ParseUint(s, 10, 64)
	return v
}
