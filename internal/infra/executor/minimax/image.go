package minimax

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/BabySid/aether/executor"
	"github.com/BabySid/aether/model"

	"aigc-platform/internal/domain/capability"
	"aigc-platform/internal/infra/executor/assetstore"
)

// costPerImageYuan is MiniMax's list price (PRD §12.1).
const costPerImageYuan = 0.025

// ImageConfig is minimax.image's declared input contract (Aether protocol
// naming: kebab-case — see client.go's package doc for why this differs
// from the MiniMax wire format used in client.go's own structs).
type ImageConfig struct {
	Prompt string `json:"prompt"`
	// N arrives as a string, not a JSON number: every value that flows
	// through workflow.parameters in this codebase is submitted as a JSON
	// string by jobsvc/Engine.Submit (its args are map[string]string), so
	// declaring N as int here would fail BindInputs' json.Unmarshal —
	// image-single.json (image.single and the old image.batch merged into
	// one workflow_name, jobsvc.go's own doc) wires n through as exactly
	// such a workflow arg.
	N           string `json:"n"`
	Model       string `json:"model"`
	AspectRatio string `json:"aspect-ratio"`
	// Seed has the same string-not-number reasoning as N above.
	Seed            string  `json:"seed"`
	StyleType       string  `json:"style-type"`
	StyleWeight     float64 `json:"style-weight"`
	PromptOptimizer *bool   `json:"prompt-optimizer"`
	AigcWatermark   *bool   `json:"aigc-watermark"`
	UserID          string  `json:"user-id"`
	// SourceImageAssetID is F5.8's image-to-image input: when set, the
	// referenced asset is downloaded and sent as MiniMax's subject_reference
	// (a data URI, not mm_file:// — subject_reference isn't listed among the
	// purposes MiniMax's file-upload API documents, so this sidesteps that
	// ambiguity entirely by inlining the bytes instead of trying to reuse the
	// video-generation-input upload/cache path).
	SourceImageAssetID string `json:"source-image-asset-id"`
	// ExpectedStyle gates an automatic-reroll quality check (checkIllustrationStyle
	// below): empty (every caller except image.comic4) skips it entirely,
	// preserving today's behavior exactly. Non-empty asks a MiniMax-M3
	// vision call whether the generated image actually reads as that style
	// rather than a real photograph, and silently rerolls with a fresh seed
	// (up to maxStyleAttempts) if it doesn't — found live off image.comic4
	// that an explicit, correctly-worded style instruction in the prompt
	// still only converted a portion of panels away from photorealism on
	// its own when anchored on a real uploaded photo.
	ExpectedStyle string `json:"expected-style"`
}

type ImagePlugin struct {
	client *Client
	sink   assetstore.Sink
	reader assetstore.Reader
}

func NewImagePlugin(client *Client, sink assetstore.Sink, reader assetstore.Reader) *ImagePlugin {
	return &ImagePlugin{client: client, sink: sink, reader: reader}
}

func (p *ImagePlugin) Type() string { return "minimax.image" }

func (p *ImagePlugin) Schema() model.ExecutorSchema {
	return executor.SchemaOf[ImageConfig, executor.DynamicOutputs](
		"minimax.image", "1.0", "MiniMax image-01 / image-01-live synchronous image generation (PRD §3.1)",
	)
}

func (p *ImagePlugin) Execute(ctx context.Context, req *executor.ExecuteRequest) (*model.ExecOutputs, error) {
	var cfg ImageConfig
	if err := executor.BindInputs(req.Inputs, &cfg); err != nil {
		return nil, fmt.Errorf("bind minimax.image inputs: %w", err)
	}
	n, _ := strconv.Atoi(cfg.N)
	if n <= 0 {
		n = 1
	}
	if n > capability.ImageMaxN {
		n = capability.ImageMaxN // MiniMax hard limit, PRD §3.1
	}
	if cfg.Model == "" {
		cfg.Model = "image-01"
	}
	if cfg.AspectRatio == "" {
		cfg.AspectRatio = "1:1"
	}
	// §5.3 step 5 (full PromptCompiler length-budget/priority truncation)
	// lands in W4; this is just a hard safety cap so we never send an
	// over-limit request and get a guaranteed 2013.
	if r := []rune(cfg.Prompt); len(r) > capability.ImageMaxPromptChars {
		cfg.Prompt = string(r[:capability.ImageMaxPromptChars])
	}

	var seed *int64
	if cfg.Seed != "" {
		if s, err := strconv.ParseInt(cfg.Seed, 10, 64); err == nil {
			seed = &s
		}
	}

	baseReq := ImageGenerationRequest{
		Model:           cfg.Model,
		Prompt:          cfg.Prompt,
		N:               n,
		AspectRatio:     cfg.AspectRatio,
		Seed:            seed,
		ResponseFormat:  "url",
		PromptOptimizer: cfg.PromptOptimizer, // F5.7: default true is applied by the caller (PromptSpec defaults), not silently here
		AigcWatermark:   cfg.AigcWatermark,
	}
	if cfg.Model == "image-01-live" && cfg.StyleType != "" {
		weight := cfg.StyleWeight
		if weight <= 0 {
			weight = 0.8
		}
		baseReq.Style = &ImageStyle{StyleType: cfg.StyleType, StyleWeight: weight}
	}
	if cfg.SourceImageAssetID != "" {
		dataURI, err := p.buildSubjectReferenceDataURI(ctx, cfg.SourceImageAssetID)
		if err != nil {
			return errOutputs(model.ExecCodeError, "source_image: "+err.Error()), nil
		}
		baseReq.SubjectReference = []SubjectReferenceItem{{Type: "character", ImageFile: dataURI}}
	}

	// maxStyleAttempts only ever matters when cfg.ExpectedStyle is set
	// (image.comic4 only) — every other caller's loop body runs exactly
	// once, identical to before this field existed.
	const maxStyleAttempts = 3
	var resp *ImageGenerationResponse
	for attempt := 1; ; attempt++ {
		mmReq := baseReq
		if attempt > 1 {
			// Rerolling because the previous attempt looked photorealistic
			// instead of the requested style — reusing the exact same seed
			// would very likely reproduce the same (rejected) image, so
			// this attempt lets MiniMax pick a fresh one. Trades away a
			// little of this one panel's seed-based consistency with its
			// siblings for actually matching the requested art style, which
			// matters more (image_comic4.go's own doc on this feature).
			mmReq.Seed = nil
		}
		var err error
		resp, err = p.client.GenerateImage(ctx, mmReq)
		if err != nil {
			// Network/transport-layer failure — Aether's retry policy handles this.
			return nil, fmt.Errorf("minimax image_generation call: %w", err)
		}

		// PRD §10.4 error classification table.
		switch resp.BaseResp.StatusCode {
		case 0:
			// continue below
		case 1002:
			return errOutputs(model.ExecCodeError, "rate_limited: "+resp.BaseResp.StatusMsg), nil
		case 1008:
			return errOutputs(model.ExecCodeFailed, "insufficient_balance: "+resp.BaseResp.StatusMsg), nil
		case 1026:
			return errOutputs(model.ExecCodeFailed, "sensitive_content: "+resp.BaseResp.StatusMsg), nil
		case 1004, 2049:
			return errOutputs(model.ExecCodeFailed, "auth_error: "+resp.BaseResp.StatusMsg), nil
		case 2013:
			return errOutputs(model.ExecCodeFailed, "bad_params: "+resp.BaseResp.StatusMsg), nil
		default:
			return errOutputs(model.ExecCodeError, fmt.Sprintf("unclassified(%d): %s", resp.BaseResp.StatusCode, resp.BaseResp.StatusMsg)), nil
		}

		if cfg.ExpectedStyle == "" || len(resp.Data.ImageURLs) == 0 {
			break // no style gate requested, or nothing to check — accept as-is, same as pre-existing behavior
		}
		stylized, checkErr := p.checkIllustrationStyle(ctx, resp.Data.ImageURLs[0], cfg.ExpectedStyle)
		if checkErr != nil || stylized || attempt >= maxStyleAttempts {
			// Accept: the check itself is advisory (an infra hiccup here
			// must never fail an otherwise-successful generation, same
			// posture as projection.go's own reviewAsset), or it genuinely
			// passed, or we're out of rerolls — whichever it is, this is
			// the image that ships.
			break
		}
	}

	// §R3: materialize immediately — these URLs expire in 24h.
	assetIDs := make([]string, 0, len(resp.Data.ImageURLs))
	for i, url := range resp.Data.ImageURLs {
		data, contentType, err := p.client.DownloadImage(ctx, url)
		if err != nil {
			// A single failed download shouldn't fail the whole batch of
			// otherwise-successful images; count it against success_count
			// so phaseConditions (n requested vs n actually usable) still
			// catches it, matching how MiniMax's own failed_count works.
			continue
		}
		width, height := imageDimensions(data)
		bizID, err := p.sink.MaterializeBytes(ctx, assetstore.NewAssetBytes{
			UserID:        parseUserID(cfg.UserID),
			Type:          "image",
			Source:        "generated",
			FromTaskRunID: req.TaskRunID,
			Body:          bytesReader(data),
			SizeBytes:     int64(len(data)),
			Ext:           extForContentType(contentType),
			Mime:          contentType,
			Width:         width,
			Height:        height,
			Meta: map[string]any{
				"model":           cfg.Model,
				"prompt":          cfg.Prompt,
				"seed":            cfg.Seed,
				"minimax_task_id": resp.ID,
				"index":           i,
			},
		})
		if err != nil {
			continue
		}
		assetIDs = append(assetIDs, bizID)
	}

	// AssetID (singular) is a convenience for n=1 callers that need to
	// reference "the one asset" without indexing into asset-ids — used by
	// image.comic4's per-panel loop, whose aggregate collects this into a
	// flat array for the compose step (a plural asset-ids field would
	// aggregate into an array-of-arrays instead).
	firstAssetID := ""
	if len(assetIDs) > 0 {
		firstAssetID = assetIDs[0]
	}

	return executor.OutputFrom(struct {
		AssetID       string   `json:"asset-id"`
		AssetIDs      []string `json:"asset-ids"`
		SuccessCount  int      `json:"success-count"`
		FailedCount   int      `json:"failed-count"`
		RequestedN    int      `json:"requested-n"`
		CostYuan      float64  `json:"cost-yuan"`
		MinimaxTaskID string   `json:"minimax-task-id"`
	}{
		AssetID:       firstAssetID,
		AssetIDs:      assetIDs,
		SuccessCount:  len(assetIDs),
		FailedCount:   n - len(assetIDs),
		RequestedN:    n,
		CostYuan:      float64(len(assetIDs)) * costPerImageYuan,
		MinimaxTaskID: resp.ID,
	})
}

// buildSubjectReferenceDataURI resolves a local asset to the data URI
// subject_reference.image_file expects (F5.8). Reuses the same
// PublicURL-then-fetch path uploadOrGetCached uses for video references,
// just without the MiniMax file-upload/cache step — inlining the bytes
// avoids needing a purpose value MiniMax's file API doesn't document for
// image generation at all.
func (p *ImagePlugin) buildSubjectReferenceDataURI(ctx context.Context, assetBizID string) (string, error) {
	url, err := p.reader.PublicURL(ctx, assetBizID)
	if err != nil {
		return "", fmt.Errorf("look up asset %s: %w", assetBizID, err)
	}
	data, err := downloadBytes(ctx, url)
	if err != nil {
		return "", fmt.Errorf("download asset %s: %w", assetBizID, err)
	}
	mime := http.DetectContentType(data)
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

// checkIllustrationStyle asks MiniMax-M3's vision input whether a
// just-generated image (still at its temporary MiniMax-hosted URL — no need
// to download/re-host it ourselves just to ask about it) reads as a real
// photograph instead of the requested illustrated style. Same "advisory,
// any error or unparseable response is treated as a pass" posture as
// projection.go's own reviewAsset (in package projection, otherwise the
// same pattern) — a false negative here just means one panel looks more
// photorealistic than asked, not a broken job; only Execute's caller
// (cfg.ExpectedStyle != "") ever invokes this at all.
func (p *ImagePlugin) checkIllustrationStyle(ctx context.Context, imageURL, expectedStyle string) (stylized bool, err error) {
	resp, err := p.client.ChatCompletion(ctx, ChatCompletionRequest{
		Model: textModel,
		Messages: []ChatMessage{{
			Role: "user",
			Content: []map[string]any{
				{"type": "image_url", "image_url": map[string]string{"url": imageURL}},
				{"type": "text", "text": "This image is supposed to be rendered in this art style: \"" + expectedStyle +
					"\". Look at it carefully. If it instead looks like a real photograph (realistic photographic " +
					"lighting/texture, not a stylized illustration), reply with exactly \"REALISTIC\" and nothing else. " +
					"If it genuinely looks like an illustrated/cartoon/anime/comic drawing matching that style, reply " +
					"with exactly \"STYLIZED\" and nothing else."},
			},
		}},
		Temperature:         0,
		MaxCompletionTokens: 20,
		Thinking:            &ThinkingConfig{Type: "disabled"},
	})
	if err != nil {
		return true, err
	}
	if len(resp.Choices) == 0 {
		return true, nil
	}
	return !strings.Contains(strings.ToUpper(resp.Choices[0].Message.Content), "REALISTIC"), nil
}

func errOutputs(code int, msg string) *model.ExecOutputs {
	return &model.ExecOutputs{Code: code, Message: msg}
}
