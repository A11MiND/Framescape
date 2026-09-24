// Package openai calls OpenAI's native Image API directly: generations for
// text-only pages, edits (multipart, reference bytes uploaded inline) when
// character/style/page references are attached.
package openai

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"aigc-platform/internal/infra/executor/assetstore"
	"aigc-platform/internal/infra/executor/spi/executor"
	"aigc-platform/internal/infra/executor/spi/model"
)

// Fixed request shape for the comic page: 3:2 canvas matching the editor's
// 1536x1024 page, and each replaced panel's 768x512 slot.
const (
	pageSize = "1536x1024"
	// maxReferences is OpenAI's edits-endpoint cap (16 images per call).
	maxReferences = 16
	// MaxReferenceBytes caps one downloaded reference; jobsvc rejects larger
	// assets before holding credits.
	MaxReferenceBytes = 20 << 20
	maxResponse       = 48 << 20
)

type Config struct {
	APIKey, Model, BaseURL string
	USDToCNY               float64
	// ReserveFor(n) = ReserveUSD + PerRefUSD*n is billed when OpenAI returns
	// an image without usage, so a paid call is never settled at zero. It
	// matches jobsvc's per-job hold for the same n.
	ReserveUSD, PerRefUSD float64
}

func (c Config) ReserveFor(refs int) float64 { return c.ReserveUSD + c.PerRefUSD*float64(refs) }

// price is USD per 1M tokens, from OpenAI's pricing page (2026-09).
type price struct{ textIn, imageIn, imageOut float64 }

var prices = []struct {
	prefix string
	p      price
}{
	{"gpt-image-2.5-flare", price{textIn: 5, imageIn: 8, imageOut: 30}},
	{"gpt-image-2.5-sunburst", price{textIn: 5, imageIn: 8, imageOut: 30}},
}

// priceFor matches a model and its dated snapshots (gpt-image-2.5-flare-2026-09-08).
func priceFor(model string) (price, bool) {
	for _, e := range prices {
		if model == e.prefix || strings.HasPrefix(model, e.prefix+"-") {
			return e.p, true
		}
	}
	return price{}, false
}

// PriceKnown reports whether cost settlement is possible for model; callers
// treat an unpriced model as "not configured" rather than billing it at zero.
func PriceKnown(model string) bool { _, ok := priceFor(model); return ok }

type ImageConfig struct {
	Prompt     string   `json:"prompt"`
	UserID     string   `json:"user-id"`
	References []string `json:"reference-image-asset-ids"`
}

type ImagePlugin struct {
	config Config
	sink   assetstore.Sink
	reader assetstore.Reader
	http   *http.Client
	fetch  *http.Client
}

func NewImagePlugin(cfg Config, sink assetstore.Sink, reader assetstore.Reader) *ImagePlugin {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.openai.com/v1"
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	noRedirect := func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return &ImagePlugin{
		config: cfg, sink: sink, reader: reader,
		http:  &http.Client{Timeout: 5 * time.Minute, CheckRedirect: noRedirect},
		fetch: &http.Client{Timeout: 30 * time.Second},
	}
}

func (p *ImagePlugin) Type() string { return "openai.image" }
func (p *ImagePlugin) Schema() model.ExecutorSchema {
	return executor.SchemaOf[ImageConfig, executor.DynamicOutputs](p.Type(), "1.0", "Direct comic page via OpenAI Image API; no automatic retry")
}

type generationResponse struct {
	Data []struct {
		Base64 string `json:"b64_json"`
	} `json:"data"`
	Usage *struct {
		InputTokens        int `json:"input_tokens"`
		OutputTokens       int `json:"output_tokens"`
		InputTokensDetails *struct {
			TextTokens  int `json:"text_tokens"`
			ImageTokens int `json:"image_tokens"`
		} `json:"input_tokens_details"`
	} `json:"usage"`
}

type result struct {
	AssetID    string   `json:"asset-id"`
	AssetIDs   []string `json:"asset-ids"`
	CostUSD    float64  `json:"cost-usd"`
	CostYuan   float64  `json:"cost-yuan"`
	UsageKnown bool     `json:"usage-known"`
	Model      string   `json:"model"`
}

type reference struct {
	data []byte
	mime string
}

func failure(message string) (*model.ExecOutputs, error) {
	return &model.ExecOutputs{Code: model.ExecCodeError, Message: message}, nil
}

func (p *ImagePlugin) Execute(ctx context.Context, req *executor.ExecuteRequest) (*model.ExecOutputs, error) {
	var cfg ImageConfig
	if err := executor.BindInputs(req.Inputs, &cfg); err != nil {
		return nil, err
	}
	uid, err := strconv.ParseUint(cfg.UserID, 10, 64)
	if err != nil || uid == 0 {
		return failure("invalid user ID")
	}
	rate := p.config.USDToCNY
	if p.config.APIKey == "" || rate <= 0 || math.IsInf(rate, 0) || math.IsNaN(rate) || p.config.ReserveUSD <= 0 {
		return failure("OpenAI is not configured")
	}
	unit, ok := priceFor(p.config.Model)
	if !ok {
		return failure("OpenAI image model " + p.config.Model + " has no configured price; refusing an unbillable call")
	}
	if strings.TrimSpace(cfg.Prompt) == "" || utf8.RuneCountInString(cfg.Prompt) > 32000 || len(cfg.References) > maxReferences {
		return failure("invalid comic prompt/reference count")
	}
	refs := make([]reference, 0, len(cfg.References))
	for _, id := range cfg.References {
		ref, err := p.loadReference(ctx, id)
		if err != nil {
			return failure("reference image unavailable: " + err.Error())
		}
		refs = append(refs, ref)
	}
	httpReq, err := p.buildRequest(ctx, cfg.Prompt, uid, refs)
	if err != nil {
		return nil, err
	}
	resp, err := p.http.Do(httpReq)
	// Never echo upstream bodies or transport errors: either may contain the
	// submitted source text or enormous image data.
	if err != nil {
		return failure("OpenAI request did not complete. Check OpenAI usage before manually retrying; no automatic retry was made.")
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if resp.StatusCode != http.StatusOK {
		return failure(upstreamError(resp.StatusCode, data))
	}
	if err != nil || len(data) > maxResponse {
		return failure("OpenAI response incomplete or too large; check OpenAI usage before retrying")
	}
	var decoded generationResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		return failure("OpenAI returned invalid JSON")
	}

	out := result{AssetIDs: []string{}, Model: p.config.Model, CostUSD: p.config.ReserveFor(len(refs))}
	var inText, inImage, outTokens int
	if u := decoded.Usage; u != nil && u.InputTokens+u.OutputTokens > 0 {
		out.UsageKnown = true
		inImage, outTokens = u.InputTokens, u.OutputTokens
		if d := u.InputTokensDetails; d != nil && d.TextTokens+d.ImageTokens > 0 {
			inText, inImage = d.TextTokens, d.ImageTokens
		}
		// Output tokens are all billed at the image-output rate; any split-out
		// text output is priced lower, so this errs on the platform's side.
		out.CostUSD = (float64(inText)*unit.textIn + float64(inImage)*unit.imageIn + float64(outTokens)*unit.imageOut) / 1e6
	}
	out.CostYuan = out.CostUSD * rate

	finishError := func(message string) (*model.ExecOutputs, error) {
		outputs, err := executor.OutputFrom(out)
		if err != nil {
			return nil, err
		}
		outputs.Code = model.ExecCodeError
		outputs.Message = message
		return outputs, nil
	}
	if len(decoded.Data) != 1 {
		return finishError("OpenAI did not return exactly one image")
	}
	imageBytes, err := base64.StdEncoding.DecodeString(decoded.Data[0].Base64)
	if err != nil {
		return finishError("OpenAI returned invalid image data")
	}
	shape, format, err := image.DecodeConfig(bytes.NewReader(imageBytes))
	if err != nil || (format != "png" && format != "jpeg") || shape.Width <= 0 || shape.Height <= 0 || shape.Width > 8192 || shape.Height > 8192 {
		return finishError("OpenAI returned an unsupported image")
	}
	ext, mime := "png", "image/png"
	if format == "jpeg" {
		ext, mime = "jpg", "image/jpeg"
	}
	assetID, err := p.sink.MaterializeBytes(ctx, assetstore.NewAssetBytes{UserID: uid, Type: "image", Source: "generated", FromTaskRunID: req.TaskRunID, Body: bytes.NewReader(imageBytes), SizeBytes: int64(len(imageBytes)), Ext: ext, Mime: mime, Width: shape.Width, Height: shape.Height, Meta: map[string]any{
		"model": p.config.Model, "prompt": cfg.Prompt, "reference_asset_ids": cfg.References, "cost_usd": out.CostUSD, "usage_known": out.UsageKnown, "usd_to_cny": rate,
		"input_text_tokens": inText, "input_image_tokens": inImage, "output_tokens": outTokens,
	}})
	if err != nil {
		return finishError("Image generated but storage failed; OpenAI usage recorded. Do not retry without checking storage/OpenAI usage.")
	}
	out.AssetID = assetID
	out.AssetIDs = []string{assetID}
	return executor.OutputFrom(out)
}

// buildRequest uses /images/generations (JSON) without references and
// /images/edits (multipart, reference bytes inline) with them. Uploading
// bytes means OpenAI never needs to reach our object storage.
func (p *ImagePlugin) buildRequest(ctx context.Context, prompt string, uid uint64, refs []reference) (*http.Request, error) {
	fields := [][2]string{
		{"model", p.config.Model}, {"prompt", prompt}, {"n", "1"}, {"size", pageSize},
		{"quality", "high"}, {"output_format", "png"}, {"background", "opaque"},
		// Hashed end-user ID lets OpenAI attribute abuse to one user, not the whole org.
		{"user", fmt.Sprintf("u-%x", sha256.Sum256([]byte("aigc-user:"+strconv.FormatUint(uid, 10))))[:34]},
	}
	var body bytes.Buffer
	var endpoint, contentType string
	if len(refs) == 0 {
		payload := map[string]any{}
		for _, f := range fields {
			payload[f[0]] = f[1]
		}
		payload["n"] = 1
		if err := json.NewEncoder(&body).Encode(payload); err != nil {
			return nil, err
		}
		endpoint, contentType = p.config.BaseURL+"/images/generations", "application/json"
	} else {
		w := multipart.NewWriter(&body)
		for _, f := range fields {
			if err := w.WriteField(f[0], f[1]); err != nil {
				return nil, err
			}
		}
		for i, ref := range refs {
			ext := strings.TrimPrefix(ref.mime, "image/")
			if ext == "jpeg" {
				ext = "jpg"
			}
			h := textproto.MIMEHeader{}
			h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="image[]"; filename="reference-%d.%s"`, i+1, ext))
			h.Set("Content-Type", ref.mime)
			part, err := w.CreatePart(h)
			if err != nil {
				return nil, err
			}
			if _, err := part.Write(ref.data); err != nil {
				return nil, err
			}
		}
		if err := w.Close(); err != nil {
			return nil, err
		}
		endpoint, contentType = p.config.BaseURL+"/images/edits", w.FormDataContentType()
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.config.APIKey)
	httpReq.Header.Set("Content-Type", contentType)
	return httpReq, nil
}

func (p *ImagePlugin) loadReference(ctx context.Context, assetID string) (reference, error) {
	u, err := p.reader.PublicURL(ctx, assetID)
	if err != nil {
		return reference{}, fmt.Errorf("asset lookup failed")
	}
	parsed, err := url.Parse(u)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.User != nil {
		return reference{}, fmt.Errorf("asset has no HTTP(S) URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return reference{}, err
	}
	resp, err := p.fetch.Do(req)
	if err != nil {
		return reference{}, fmt.Errorf("download failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return reference{}, fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxReferenceBytes+1))
	if err != nil || len(data) > MaxReferenceBytes {
		return reference{}, fmt.Errorf("download incomplete or larger than 20 MB")
	}
	mime := http.DetectContentType(data)
	if mime != "image/png" && mime != "image/jpeg" && mime != "image/webp" {
		return reference{}, fmt.Errorf("must be PNG, JPEG or WebP")
	}
	return reference{data: data, mime: mime}, nil
}

var safeCode = regexp.MustCompile(`^[a-z0-9_.-]{1,64}$`)

// upstreamError reports only OpenAI's machine-readable error code, never the
// free-text message (which can quote the prompt). Moderation rejections use
// the "sensitive_content:" prefix projection keys moderation_records off.
func upstreamError(status int, body []byte) string {
	var e struct {
		Error struct {
			Code any    `json:"code"`
			Type string `json:"type"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &e)
	code, _ := e.Error.Code.(string)
	if !safeCode.MatchString(code) {
		code = ""
	}
	if code == "moderation_blocked" || code == "content_policy_violation" {
		return "sensitive_content: OpenAI rejected the prompt or a reference image (" + code + ")"
	}
	if code == "" && safeCode.MatchString(e.Error.Type) {
		code = e.Error.Type
	}
	if code != "" {
		return fmt.Sprintf("OpenAI returned HTTP %d (%s); no automatic retry was made", status, code)
	}
	return fmt.Sprintf("OpenAI returned HTTP %d; no automatic retry was made", status)
}
