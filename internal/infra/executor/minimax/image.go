package minimax

import (
	"context"
	"fmt"
	"strconv"

	"github.com/BabySid/aether/executor"
	"github.com/BabySid/aether/model"

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
	// declaring N as int here would fail BindInputs' json.Unmarshal whenever
	// it's wired from a workflow arg (image-batch.json) rather than a
	// literal task input (image-single.json, which also uses a quoted
	// string literal for exactly this reason — see that file).
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
}

type ImagePlugin struct {
	client *Client
	sink   assetstore.Sink
}

func NewImagePlugin(client *Client, sink assetstore.Sink) *ImagePlugin {
	return &ImagePlugin{client: client, sink: sink}
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
	if n > 9 {
		n = 9 // MiniMax hard limit, PRD §3.1
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
	if r := []rune(cfg.Prompt); len(r) > 1500 {
		cfg.Prompt = string(r[:1500])
	}

	var seed *int64
	if cfg.Seed != "" {
		if s, err := strconv.ParseInt(cfg.Seed, 10, 64); err == nil {
			seed = &s
		}
	}

	mmReq := ImageGenerationRequest{
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
		mmReq.Style = &ImageStyle{StyleType: cfg.StyleType, StyleWeight: weight}
	}

	resp, err := p.client.GenerateImage(ctx, mmReq)
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

func errOutputs(code int, msg string) *model.ExecOutputs {
	return &model.ExecOutputs{Code: code, Message: msg}
}
