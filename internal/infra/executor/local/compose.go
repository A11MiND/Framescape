// Package local implements executors that don't call any external provider
// (PRD §10.1: local.compose/local.ffmpeg.*/local.moderation). compose.go is
// F5.3's "自动拼版": tiles N comic-panel images into one grid. Grid
// dimensions are computed from however many asset IDs actually arrive,
// nearly-square (ceil(sqrt(n)) columns), since image.comic4's panel count
// is not fixed at 4.
package local

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	_ "image/png" // decoder registration (jpeg is imported non-blank below since we also Encode with it)
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/BabySid/aether/executor"
	"github.com/BabySid/aether/model"
	"go.uber.org/zap"

	"aigc-platform/internal/infra/executor/assetstore"
	"aigc-platform/internal/pkg/logger"
)

// tileSize: each panel is resized to a tileSize x tileSize square before
// tiling. Was 768 — a plain downscale for most MiniMax image-01 output
// (typically >=1024 on a side for a square ratio), needlessly softening
// detail in the composed grid before it was ever shown to anyone
// ("四格漫画最终合成出来的图片应该大一点", found live). 1024 matches that
// native resolution rather than fighting it — scaleToSquare still handles
// anything actually smaller without introducing new upscale artifacts
// beyond what a smaller source already has.
const tileSize = 1024

// layoutCanvasSize is the overall square canvas used by every named,
// non-equal-grid layout (layoutRects below) — unlike composeGrid's own
// tileSize*cols/rows sizing (which only ever needs equal square cells),
// these layouts mix panel sizes/shapes on one canvas, so the canvas itself
// has to be a fixed size the panel rects are fractions of.
const layoutCanvasSize = 2048

type ComposeConfig struct {
	AssetIDs []string `json:"asset-ids"`
	// Layout selects a named panel-layout template (image_comic4.go's AI
	// planner picks one of minimax.LayoutGridEqual/FeatureLast/FeatureFirst/
	// VerticalStrip/HorizontalStrip — this package doesn't import minimax
	// for that, it just matches the same string values, see layoutRects).
	// "" or any value layoutRects doesn't recognize (including
	// "grid-equal" itself) falls back to composeGrid's original
	// always-equal-square-grid behavior, so an older/blank job never
	// regresses.
	Layout string `json:"layout"`
	UserID string `json:"user-id"`
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

	var canvas *image.RGBA
	var layoutMeta string
	if rects := layoutRects(cfg.Layout, len(tiles)); rects != nil {
		canvas = composeCustomLayout(tiles, rects)
		layoutMeta = cfg.Layout
	} else {
		var cols, rows int
		canvas, cols, rows = composeGrid(tiles)
		layoutMeta = fmt.Sprintf("%dx%d", cols, rows)
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, canvas, &jpeg.Options{Quality: 90}); err != nil {
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
		Width:         canvas.Bounds().Dx(),
		Height:        canvas.Bounds().Dy(),
		Meta: map[string]any{
			"composed_from": cfg.AssetIDs,
			"layout":        layoutMeta,
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
	// mock.image's own PublicURL is a data: URI, not a real provider URL —
	// net/http.Client can't fetch that scheme, so decode it directly.
	if strings.HasPrefix(url, "data:") {
		return decodeDataURIImage(url)
	}

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

// decodeDataURIImage decodes a "data:<mime>;base64,<payload>" URI in memory
// (RFC 2397; bare, non-base64 encodings are not supported).
func decodeDataURIImage(uri string) (image.Image, error) {
	rest, ok := strings.CutPrefix(uri, "data:")
	if !ok {
		return nil, fmt.Errorf("not a data URI")
	}
	meta, payload, ok := strings.Cut(rest, ",")
	if !ok || !strings.HasSuffix(meta, ";base64") {
		return nil, fmt.Errorf("unsupported data URI encoding (want base64)")
	}
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return nil, fmt.Errorf("decode base64 data URI: %w", err)
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	return img, err
}

// composeGrid tiles N images into a nearly-square canvas — columns =
// ceil(sqrt(n)), rows = ceil(n/columns), so 4 still lands on the original
// 2x2, 6 becomes 3x2, 9 becomes 3x3, and an odd count like 5 or 7 gets a
// trailing partial row rather than failing or silently dropping panels
// (image.comic4 no longer promises exactly 4, see this file's package
// doc). Each source image is scaled to fill a tileSize square via
// nearest-neighbor (fine for POC; a real resampler is a cosmetic upgrade,
// not a correctness one).
func composeGrid(tiles []image.Image) (canvas *image.RGBA, cols, rows int) {
	n := len(tiles)
	cols = int(math.Ceil(math.Sqrt(float64(n))))
	if cols < 1 {
		cols = 1
	}
	rows = (n + cols - 1) / cols

	canvas = image.NewRGBA(image.Rect(0, 0, tileSize*cols, tileSize*rows))
	for i, tile := range tiles {
		col, row := i%cols, i/cols
		scaled := scaleToSquare(tile, tileSize)
		dstRect := image.Rect(col*tileSize, row*tileSize, (col+1)*tileSize, (row+1)*tileSize)
		draw.Draw(canvas, dstRect, scaled, image.Point{}, draw.Src)
	}
	return canvas, cols, rows
}

// layoutRect is one panel's position on the canvas, as a fraction (0..1) of
// the canvas's overall width/height — resolution-independent so the same
// template works at any layoutCanvasSize.
type layoutRect struct{ X, Y, W, H float64 }

// layoutRects returns exactly one rect per tile for a recognized named
// layout, or nil for "grid-equal"/blank/unrecognized values — the nil case
// tells Execute to fall back to composeGrid's original equal-square-grid
// math unchanged, which is deliberate: this only needs to cover the layouts
// image_comic4.go's AI planner is actually allowed to choose (its own
// whitelist), everything else keeps today's exact behavior.
func layoutRects(layoutID string, n int) []layoutRect {
	if n <= 0 {
		return nil
	}
	switch layoutID {
	case "feature-last":
		return featureRects(n, true)
	case "feature-first":
		return featureRects(n, false)
	case "vertical-strip":
		return stripRects(n, true)
	case "horizontal-strip":
		return stripRects(n, false)
	default:
		return nil
	}
}

// stripRects lays out n panels as equal-width rows (vertical) or
// equal-height columns (horizontal) — generic for any n, unlike
// composeGrid's near-square packing.
func stripRects(n int, vertical bool) []layoutRect {
	rects := make([]layoutRect, n)
	frac := 1.0 / float64(n)
	for i := range rects {
		if vertical {
			rects[i] = layoutRect{X: 0, Y: float64(i) * frac, W: 1, H: frac}
		} else {
			rects[i] = layoutRect{X: float64(i) * frac, Y: 0, W: frac, H: 1}
		}
	}
	return rects
}

// featureRects gives one panel (the last one if last=true, else the first)
// a full-width half of the canvas, and splits the remaining half evenly
// among every other panel — the classic "punchline/opening panel is bigger"
// comic convention (Canva's own comic-layout guidance calls out exactly this
// kind of unequal grid). Generic for any n>=1; n==1 degenerates to a single
// full-canvas rect.
func featureRects(n int, last bool) []layoutRect {
	if n <= 1 {
		return []layoutRect{{X: 0, Y: 0, W: 1, H: 1}}
	}
	rects := make([]layoutRect, n)
	featuredIdx, restStart, featuredY, restY := 0, 1, 0.0, 0.5
	if last {
		featuredIdx, restStart, featuredY, restY = n-1, 0, 0.5, 0.0
	}
	rects[featuredIdx] = layoutRect{X: 0, Y: featuredY, W: 1, H: 0.5}
	restFrac := 1.0 / float64(n-1)
	slot := 0
	for i := restStart; i < restStart+n-1; i++ {
		rects[i] = layoutRect{X: float64(slot) * restFrac, Y: restY, W: restFrac, H: 0.5}
		slot++
	}
	return rects
}

// composeCustomLayout renders tiles onto one layoutCanvasSize-square canvas
// per the given rects (same length/order as tiles) — layoutRects' own
// caller (Execute) guarantees that pairing.
func composeCustomLayout(tiles []image.Image, rects []layoutRect) *image.RGBA {
	canvas := image.NewRGBA(image.Rect(0, 0, layoutCanvasSize, layoutCanvasSize))
	for i, tile := range tiles {
		r := rects[i]
		x0, y0 := int(r.X*layoutCanvasSize), int(r.Y*layoutCanvasSize)
		w, h := int(r.W*layoutCanvasSize), int(r.H*layoutCanvasSize)
		scaled := scaleToRect(tile, w, h)
		draw.Draw(canvas, image.Rect(x0, y0, x0+w, y0+h), scaled, image.Point{}, draw.Src)
	}
	return canvas
}

// scaleToRect is scaleToSquare generalized to a non-square target — the
// same nearest-neighbor approach (fine for POC, same caveat as
// scaleToSquare's own doc).
func scaleToRect(src image.Image, w, h int) image.Image {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	sb := src.Bounds()
	for y := 0; y < h; y++ {
		sy := sb.Min.Y + y*sb.Dy()/h
		for x := 0; x < w; x++ {
			sx := sb.Min.X + x*sb.Dx()/w
			dst.Set(x, y, src.At(sx, sy))
		}
	}
	return dst
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
