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
	// ExpectedDialogue gates a second automatic-reroll check, same posture
	// as ExpectedStyle: empty (every panel with no speech bubble, and every
	// caller outside image.comic4) skips it entirely. Non-empty asks
	// whether the speech-bubble text actually rendered matches this exact
	// line, and rerolls if it doesn't — found live that a comic panel's
	// bubble can render confident-looking but genuinely garbled text (e.g.
	// "When will I pou leld?" for an intended English line), which a
	// style-only check has no way to catch since the panel can otherwise be
	// a perfectly on-style illustration.
	ExpectedDialogue string `json:"expected-dialogue"`
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
	var referenceDataURI string
	if cfg.SourceImageAssetID != "" {
		dataURI, err := p.buildSubjectReferenceDataURI(ctx, cfg.SourceImageAssetID)
		if err != nil {
			return errOutputs(model.ExecCodeError, "source_image: "+err.Error()), nil
		}
		baseReq.SubjectReference = []SubjectReferenceItem{{Type: "character", ImageFile: dataURI}}
		referenceDataURI = dataURI // reused below for checkIdentityMatch — no second download
	}

	// maxStyleAttempts only ever matters when cfg.ExpectedStyle is set
	// (image.comic4 only) — every other caller's loop body runs exactly
	// once, identical to before this field existed.
	// checkIdentity gates the identity-consistency reroll (checkIdentityMatch
	// below): only meaningful when there's an actual reference image to
	// compare against (SourceImageAssetID) and the caller is asking for
	// automatic quality checks at all (ExpectedStyle — set only by
	// image.comic4, same scoping every other check here already uses).
	checkIdentity := cfg.ExpectedStyle != "" && referenceDataURI != ""

	const maxStyleAttempts = 3
	var resp *ImageGenerationResponse
	for attempt := 1; ; attempt++ {
		mmReq := baseReq
		if attempt > 1 && seed != nil {
			// Rerolling because the previous attempt failed a quality check
			// — reusing the exact same seed would very likely reproduce the
			// same (rejected) image. A *fully* random reseed here used to
			// undo the whole point of image_comic4's shared per-job seed:
			// found live that a panel needing even one reroll could end up
			// looking like a visibly different character from its
			// (non-rerolled) siblings, since it alone lost the shared seed
			// neighborhood. A small deterministic offset still gives
			// MiniMax a genuinely different sample to try while staying
			// close enough to the original seed's own visual identity.
			s := *seed + int64(attempt)
			mmReq.Seed = &s
		} else if attempt > 1 {
			mmReq.Seed = nil // no shared seed was requested in the first place — nothing to stay close to
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

		if (cfg.ExpectedStyle == "" && cfg.ExpectedDialogue == "" && !checkIdentity) || len(resp.Data.ImageURLs) == 0 {
			break // no quality gate requested, or nothing to check — accept as-is, same as pre-existing behavior
		}
		passed := true
		if cfg.ExpectedStyle != "" {
			ok, checkErr := p.checkIllustrationStyle(ctx, resp.Data.ImageURLs[0], cfg.ExpectedStyle)
			passed = checkErr != nil || ok // an infra hiccup here is advisory-pass, same posture as projection.go's own reviewAsset
		}
		// Every later check below only runs once the earlier ones already
		// passed — this attempt is getting rerolled either way once any one
		// check fails, so there's no point spending another vision call
		// finding out whether the others also happen to be wrong.
		if passed && checkIdentity {
			ok, checkErr := p.checkIdentityMatch(ctx, resp.Data.ImageURLs[0], referenceDataURI)
			passed = checkErr != nil || ok
		}
		if passed && cfg.ExpectedDialogue != "" {
			ok, checkErr := p.checkDialogueText(ctx, resp.Data.ImageURLs[0], cfg.ExpectedDialogue)
			passed = checkErr != nil || ok
		}
		if passed || attempt >= maxStyleAttempts {
			// Accept: both requested checks passed (or weren't requested),
			// or we're out of rerolls — either way, this is the image that
			// ships.
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
// to download/re-host it ourselves just to ask about it) actually matches
// the requested art style specifically, not just "any non-photo style".
// Found live that a looser photo-vs-illustration binary let a 3D-rendered
// CGI/Pixar-style panel pass right alongside sibling panels correctly
// rendered as flat 2D cel-shaded anime — a real style-consistency defect
// across the same comic, even though no individual panel was "photorealistic"
// on its own. Same "advisory, any error or unparseable response is treated
// as a pass" posture as projection.go's own reviewAsset (in package
// projection, otherwise the same pattern) — a false negative here just means
// one panel looks stylistically off, not a broken job; only Execute's
// caller (cfg.ExpectedStyle != "") ever invokes this at all.
func (p *ImagePlugin) checkIllustrationStyle(ctx context.Context, imageURL, expectedStyle string) (stylized bool, err error) {
	resp, err := p.client.ChatCompletion(ctx, ChatCompletionRequest{
		Model: textModel,
		Messages: []ChatMessage{{
			Role: "user",
			Content: []map[string]any{
				{"type": "image_url", "image_url": map[string]string{"url": imageURL}},
				{"type": "text", "text": "This image is supposed to be rendered in exactly this art style: \"" + expectedStyle +
					"\". Look at it carefully and judge strictly. Reply with exactly \"MISMATCH\" and nothing else if ANY of " +
					"these are true: it looks like a real photograph rather than a drawing; it is a 3D-rendered / CGI / " +
					"Pixar-like style when the requested style calls for flat 2D illustration (or vice versa); its line " +
					"work, shading, or overall rendering technique otherwise clearly does not match the requested style " +
					"description. Reply with exactly \"MATCH\" and nothing else only if it genuinely looks like it was " +
					"drawn in that specific style."},
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
	return !strings.Contains(strings.ToUpper(resp.Choices[0].Message.Content), "MISMATCH"), nil
}

// checkDialogueText asks MiniMax-M3's vision input to actually read the
// speech-bubble text in a just-generated image and compare it against the
// intended line. Found live that a comic panel's bubble can render
// confident-looking but genuinely garbled text (e.g. "When will I pou
// leld?" for an intended English line) — a defect a style check has no way
// to catch, since the panel can otherwise be a perfectly on-style
// illustration. Same "advisory, any error or unparseable response is a
// pass" posture as checkIllustrationStyle — only Execute's caller
// (cfg.ExpectedDialogue != "") ever invokes this at all.
func (p *ImagePlugin) checkDialogueText(ctx context.Context, imageURL, expectedDialogue string) (matches bool, err error) {
	resp, err := p.client.ChatCompletion(ctx, ChatCompletionRequest{
		Model: textModel,
		Messages: []ChatMessage{{
			Role: "user",
			Content: []map[string]any{
				{"type": "image_url", "image_url": map[string]string{"url": imageURL}},
				{"type": "text", "text": "This image should contain a speech bubble whose text reads exactly: \"" + expectedDialogue +
					"\". Read the actual text rendered inside the bubble carefully, letter by letter. Reply with exactly " +
					"\"MISMATCH\" and nothing else if the bubble is missing, illegible, garbled, or the text differs from " +
					"the intended line in any way (extra/missing/wrong letters, nonsense words, wrong language). Reply with " +
					"exactly \"MATCH\" and nothing else only if the rendered text is clearly legible and reads correctly " +
					"(minor case/punctuation differences are fine)."},
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
	return !strings.Contains(strings.ToUpper(resp.Choices[0].Message.Content), "MISMATCH"), nil
}

// checkIdentityMatch asks MiniMax-M3's vision input whether a just-generated
// panel actually depicts the same character as the reference image it was
// anchored on. Found live that image.comic4's panels — even though every
// one is independently anchored on the identical reference image, not
// chained off each other — could still visibly diverge from one another:
// specifically, a panel that needed even one style/dialogue reroll lost its
// shared per-job seed (this file's own Execute doc on that), and the
// resulting fresh sample sometimes drifted far enough from the reference
// that it read as a different individual (wrong face shape, wrong
// coloring/markings) rather than just a different pose of the same one.
// subject_reference itself doesn't guarantee this — see comic_plan.go's
// DescribeReferenceSubject doc on why it's documented as portrait-tuned,
// not a hard identity lock for every subject. referenceDataURI is the exact
// same data URI already sent as this call's own subject_reference, reused
// here rather than re-downloaded. Same "advisory, any error or unparseable
// response is a pass" posture as this file's other checks.
func (p *ImagePlugin) checkIdentityMatch(ctx context.Context, generatedURL, referenceDataURI string) (matches bool, err error) {
	resp, err := p.client.ChatCompletion(ctx, ChatCompletionRequest{
		Model: textModel,
		Messages: []ChatMessage{{
			Role: "user",
			Content: []map[string]any{
				{"type": "text", "text": "Image 1 is the reference character:"},
				{"type": "image_url", "image_url": map[string]string{"url": referenceDataURI}},
				{"type": "text", "text": "Image 2 is a newly generated panel that is supposed to depict the exact same character:"},
				{"type": "image_url", "image_url": map[string]string{"url": generatedURL}},
				{"type": "text", "text": "Compare the main character's face/head in both images — same species/type, same coloring or " +
					"markings, same distinguishing features, same general build. Ignore differences in pose, expression, camera " +
					"angle, art style, or background — only judge whether it's plausibly the same individual. Reply with exactly " +
					"\"MISMATCH\" and nothing else if image 2's character looks like a visibly different individual (wrong " +
					"coloring/markings, different face shape, different species/type). Reply with exactly \"MATCH\" and nothing " +
					"else if it's a reasonable depiction of the same character."},
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
	return !strings.Contains(strings.ToUpper(resp.Choices[0].Message.Content), "MISMATCH"), nil
}

func errOutputs(code int, msg string) *model.ExecOutputs {
	return &model.ExecOutputs{Code: code, Message: msg}
}
