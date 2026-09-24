package jobsvc

import (
	"context"
	"fmt"

	"aigc-platform/internal/application/workflows"
	"aigc-platform/internal/domain/prompt"
	"aigc-platform/internal/domain/workflow"
)

// validateShotSourceRefs rejects forward or self references, which would be
// dependency cycles.
func validateShotSourceRefs(refs []int, shotCount int) error {
	for i, r := range refs {
		if r == 0 {
			continue
		}
		if i >= shotCount {
			return fmt.Errorf("shot_source_refs has more entries than shots")
		}
		if r < 1 || r > i {
			return fmt.Errorf("shot %d's source reference must point at an earlier shot (1..%d), got %d", i+1, i, r)
		}
	}
	return nil
}

func (s *Service) prepareImageSequence(ctx context.Context, userID uint64, spec Spec) (*workflow.Plan, string, error) {
	if len(spec.Shots) == 0 {
		return nil, "", fmt.Errorf("image.sequence requires at least 1 shot")
	}
	if err := validateShotSourceRefs(spec.ShotSourceRefs, len(spec.Shots)); err != nil {
		return nil, "", err
	}
	characters, err := s.resolveCharacters(ctx, userID, spec.Characters)
	if err != nil {
		return nil, "", err
	}
	presets, err := s.resolvePresets(ctx, spec.PresetIDs)
	if err != nil {
		return nil, "", err
	}
	seed := resolveSharedSeed(characters, spec.Seed)
	shots := make([]workflows.SequenceShot, len(spec.Shots))
	for i, text := range spec.Shots {
		compiled := prompt.Compile(prompt.Input{Text: text, Characters: characters, Presets: presets, Seed: seed})
		ref := 0
		if i < len(spec.ShotSourceRefs) {
			ref = spec.ShotSourceRefs[i]
		}
		if ref == 0 && i > 0 && spec.ImageSequenceMode == "continuity" {
			ref = i
		}
		shots[i] = workflows.SequenceShot{Index: i + 1, Prompt: compiled.Prompt, Seed: formatSeed(seed), Reference: spec.SourceImageAssetID, RefShot: ref}
	}
	plan, err := workflows.ImageSequencePlan(userID, normalizeImageProvider(spec.ImageProvider), shots)
	return plan, spec.Shots[0], err
}
