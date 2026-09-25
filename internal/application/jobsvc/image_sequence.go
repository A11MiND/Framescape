package jobsvc

import (
	"aigc-platform/internal/pkg/apperr"
	"context"
	"fmt"

	"aigc-platform/internal/application/workflows"
	"aigc-platform/internal/domain/capability"
	"aigc-platform/internal/domain/prompt"
	"aigc-platform/internal/domain/workflow"
)

// checkSequenceShots bounds the number of shots, each of which is one
// provider call.
func checkSequenceShots(n int) error {
	if n == 0 {
		return errShotsRequired
	}
	if n > capability.ImageSequenceMaxShots {
		return apperr.New("shots_too_many", fmt.Sprintf("at most %d shots per image sequence", capability.ImageSequenceMaxShots), "max", capability.ImageSequenceMaxShots)
	}
	return nil
}

// validateShotSourceRefs rejects forward or self references, which would be
// dependency cycles.
func validateShotSourceRefs(refs []int, shotCount int) error {
	for i, r := range refs {
		if r == 0 {
			continue
		}
		if i >= shotCount {
			return apperr.New("shot_refs_invalid", "shot_source_refs has more entries than shots")
		}
		if r < 1 || r > i {
			return apperr.New("shot_refs_invalid", fmt.Sprintf("shot %d's source reference must point at an earlier shot (1..%d), got %d", i+1, i, r), "shot", i+1)
		}
	}
	return nil
}

func (s *Service) prepareImageSequence(ctx context.Context, userID uint64, spec Spec) (*workflow.Plan, string, error) {
	if err := checkSequenceShots(len(spec.Shots)); err != nil {
		return nil, "", err
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
	m := workflows.ImageModel{Provider: normalizeImageProvider(spec.ImageProvider)}
	if m.Provider == workflows.ProviderOpenAI {
		var static []string
		if spec.SourceImageAssetID != "" {
			static = []string{spec.SourceImageAssetID}
		}
		o, err := s.openAIPrepare(ctx, userID, "image.sequence", spec, static)
		if err != nil {
			return nil, "", err
		}
		m.Size, m.Quality = o.Size, o.Quality
	}
	plan, err := workflows.ImageSequencePlan(userID, m, shots)
	return plan, spec.Shots[0], err
}
