package jobsvc

import (
	"context"
	"encoding/json"

	"aigc-platform/internal/domain/workflow"
	"aigc-platform/internal/pkg/apperr"
)

// Preview is what a single-call image request would send to the provider:
// the compiled prompt (characters and presets expanded) and the reference
// images, in order.
type Preview struct {
	Prompt     string   `json:"prompt"`
	References []string `json:"references"`
}

// PreviewRequest builds the request exactly as Create would, without
// submitting or reserving anything. Only workflows whose preparation makes
// no provider call are previewable.
func (s *Service) PreviewRequest(ctx context.Context, userID uint64, workflowName string, spec Spec) (*Preview, error) {
	if err := checkProvider(workflowName, spec); err != nil {
		return nil, err
	}
	var (
		plan *workflow.Plan
		err  error
	)
	switch {
	case workflowName == "image.single":
		plan, _, err = s.prepareImageSingle(ctx, userID, spec)
	case workflowName == "image.comic4" && spec.ComicMode != "":
		plan, _, err = s.prepareDirectComic(ctx, userID, spec)
	default:
		return nil, ErrNotSupported
	}
	if err != nil {
		return nil, err
	}
	for _, n := range plan.Nodes {
		if n.Display["result"] != true {
			continue
		}
		p := &Preview{References: []string{}}
		_ = literalInput(n.Inputs["prompt"], &p.Prompt)
		var refs []string
		if literalInput(n.Inputs["reference-image-asset-ids"], &refs) && len(refs) > 0 {
			p.References = refs
		} else {
			var one string
			if literalInput(n.Inputs["source-image-asset-id"], &one) && one != "" {
				p.References = []string{one}
			}
		}
		return p, nil
	}
	return nil, apperr.New("not_supported", "the request has no previewable step")
}

func literalInput(in workflow.Input, out any) bool {
	return len(in.Value) > 0 && json.Unmarshal(in.Value, out) == nil
}
