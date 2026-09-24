package jobsvc

import (
	"aigc-platform/internal/pkg/apperr"
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"aigc-platform/internal/application/workflows"
	"aigc-platform/internal/domain/capability"
	"aigc-platform/internal/domain/prompt"
	"aigc-platform/internal/infra/persistence"
)

// retryableNodes lists the nodes that can be re-run on their own: the node's
// inputs must be reconstructible from the job's spec alone.
var retryableNodes = map[string]string{
	"video.single": "gen",
}

// RetryNode re-runs one failed node as a new job linked to the original
// (retry_of_*). The original job stays as it was.
func (s *Service) RetryNode(ctx context.Context, userID uint64, bizID, nodeName string, loopIndex int, promptOverride string) (*persistence.Job, error) {
	job, run, err := s.Get(ctx, userID, bizID)
	if err != nil {
		return nil, err
	}
	if retryableNodes[job.WorkflowName] != nodeName || loopIndex != -1 {
		return nil, apperr.New("not_supported", fmt.Sprintf("node %q is not retryable for workflow %q", nodeName, job.WorkflowName))
	}
	failed := false
	for _, n := range run.Nodes {
		if n.Name == nodeName && slices.Contains([]string{"Failed", "Error", "Timeout"}, n.Phase) {
			failed = true
		}
	}
	if !failed {
		return nil, apperr.New("node_not_failed", fmt.Sprintf("node %q has not failed; nothing to retry", nodeName))
	}

	var spec Spec
	if err := json.Unmarshal(job.Spec, &spec); err != nil {
		return nil, fmt.Errorf("decode job spec: %w", err)
	}
	if spec.Resolution != "" && !slices.Contains(capability.VideoResolutions, spec.Resolution) {
		return nil, errResolution(spec.Resolution)
	}
	characters, err := s.resolveCharacters(ctx, userID, spec.Characters)
	if err != nil {
		return nil, err
	}
	presets, err := s.resolvePresets(ctx, spec.PresetIDs)
	if err != nil {
		return nil, err
	}
	text := spec.Text
	if promptOverride != "" {
		text = promptOverride
	}
	compiled := prompt.Compile(prompt.Input{Text: text, Characters: characters, Presets: presets, Seed: spec.Seed, MaxChars: capability.VideoMaxPromptChars})
	refImages := spec.ReferenceImageAssetIDs
	if len(refImages) == 0 && spec.FirstFrameAssetID == "" && spec.LastFrameAssetID == "" && len(spec.Characters) > 0 {
		if refImages, err = s.resolveCharacterRefAssetIDs(ctx, userID, spec.Characters); err != nil {
			return nil, err
		}
	}
	duration, resolution := videoDuration(spec.DurationSeconds), videoResolution(spec.Resolution)
	// The retry re-runs generation only, never the optional enhancement step.
	plan := workflows.VideoSinglePlan(workflows.VideoSingle{
		UserID: userID, Prompt: compiled.Prompt, Duration: duration, Resolution: resolution, Ratio: spec.Ratio,
		FirstFrame: spec.FirstFrameAssetID, LastFrame: spec.LastFrameAssetID, ReferenceImages: nonNil(refImages),
		ReferenceVideos: nonNil(spec.ReferenceVideoAssetIDs), ReferenceAudios: nonNil(spec.ReferenceAudioAssetIDs),
	})
	retrySpec := spec
	retrySpec.Text, retrySpec.PromptEnhance = text, false
	estimate, err := EstimateCredits("video.single", retrySpec)
	if err != nil {
		return nil, err
	}
	return s.submit(ctx, submission{
		userID: userID, workflowName: job.WorkflowName, spec: retrySpec, title: job.Title, estimate: estimate,
		plan: plan, holdKind: "retry", retryOf: job, retryNode: nodeName,
	})
}
