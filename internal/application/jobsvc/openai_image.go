package jobsvc

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"aigc-platform/internal/application/creditsvc"
	"aigc-platform/internal/application/workflows"
	"aigc-platform/internal/domain/capability"
	"aigc-platform/internal/domain/comic"
	"aigc-platform/internal/infra/executor/openai"
	"aigc-platform/internal/infra/persistence"
	"aigc-platform/internal/pkg/config"
)

// ErrOpenAINotEnabled is returned for accounts outside the OpenAI image gray
// release (the openai_image entitlement).
var ErrOpenAINotEnabled = errors.New("OpenAI image generation is in limited beta and not enabled for this account")

// ErrOpenAIUnavailable is returned when this deployment cannot run OpenAI.
var ErrOpenAIUnavailable = errors.New("OpenAI image generation is not configured on this deployment")

// OpenAIImageEnabled gates job creation and GET /capabilities: a key plus a
// model the executor can price (an unpriced model would settle at zero).
func OpenAIImageEnabled() bool {
	return config.OpenAIAPIKey() != "" && openai.PriceKnown(config.OpenAIImageModel())
}

// OpenAIAllowed is the gray-release gate: admins, plus accounts granted the
// openai_image entitlement.
func (s *Service) OpenAIAllowed(ctx context.Context, userID uint64) (bool, error) {
	var u persistence.User
	if err := s.db.WithContext(ctx).Select("is_admin").First(&u, userID).Error; err != nil {
		return false, err
	}
	if u.IsAdmin {
		return true, nil
	}
	return persistence.HasEntitlement(ctx, s.db, userID, persistence.EntitlementOpenAIImage)
}

func (s *Service) requireOpenAI(ctx context.Context, userID uint64) error {
	if !OpenAIImageEnabled() {
		return ErrOpenAIUnavailable
	}
	allowed, err := s.OpenAIAllowed(ctx, userID)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrOpenAINotEnabled
	}
	return nil
}

// openAIPrepare checks access, options and the references of a general
// OpenAI generation.
func (s *Service) openAIPrepare(ctx context.Context, userID uint64, workflowName string, spec Spec, refs []string) (openAIOptions, error) {
	if err := s.requireOpenAI(ctx, userID); err != nil {
		return openAIOptions{}, err
	}
	o, err := openAIImageOptions(workflowName, spec)
	if err != nil {
		return o, err
	}
	return o, s.checkOpenAIRefs(ctx, userID, refs)
}

// checkOpenAIRefs verifies every reference image can be sent to OpenAI.
func (s *Service) checkOpenAIRefs(ctx context.Context, userID uint64, refs []string) error {
	if len(refs) > openai.MaxReferences {
		return fmt.Errorf("at most %d reference images can be sent to OpenAI", openai.MaxReferences)
	}
	for _, ref := range refs {
		var asset persistence.Asset
		if err := s.db.WithContext(ctx).Where("biz_id = ? AND user_id = ? AND deleted_at IS NULL AND type = ?", ref, userID, "image").First(&asset).Error; err != nil {
			return fmt.Errorf("reference image unavailable")
		}
		if !comic.ImageMime(asset.Mime) {
			return fmt.Errorf("reference images must be PNG, JPEG or WebP")
		}
		if asset.SizeBytes > openai.MaxReferenceBytes {
			return fmt.Errorf("reference images must be at most 20 MB")
		}
		if asset.PublicURL == "" {
			return fmt.Errorf("reference image upload is incomplete")
		}
	}
	return nil
}

// checkProvider rejects provider/workflow combinations that do not exist.
func checkProvider(workflowName string, spec Spec) error {
	if spec.ImageProvider != workflows.ProviderOpenAI {
		if spec.ImageSize != "" || spec.ImageQuality != "" {
			return fmt.Errorf("image_size and image_quality apply to image_provider=openai only")
		}
		return nil
	}
	switch {
	case workflowName == "image.single", workflowName == "image.sequence":
		_, err := openAIImageOptions(workflowName, spec)
		return err
	case workflowName == "image.comic4" && spec.ComicMode != "":
		return nil
	}
	return fmt.Errorf("image_provider=openai supports image.single, image.sequence and the direct or editable comic")
}

// openAIOptions is what one general OpenAI generation call asks for.
type openAIOptions struct {
	N       int
	Size    string
	Quality string
}

// openAIImageOptions resolves and validates the request's size, quality and
// count against the configured options. The size defaults from the aspect
// ratio; the quality defaults to high, as for comic pages.
func openAIImageOptions(workflowName string, spec Spec) (openAIOptions, error) {
	o := openAIOptions{N: 1, Size: spec.ImageSize, Quality: spec.ImageQuality}
	if workflowName == "image.single" && spec.N > 0 {
		o.N = spec.N
	}
	if o.Size == "" {
		o.Size = sizeForRatio(spec.AspectRatio)
	}
	if o.Quality == "" {
		o.Quality = openai.DefaultQuality
	}
	if maxN := min(config.OpenAIImageMaxN(), capability.ImageMaxN); o.N > maxN {
		return o, fmt.Errorf("OpenAI generates at most %d images per job", maxN)
	}
	if !slices.Contains(config.OpenAIImageSizes(), o.Size) {
		return o, fmt.Errorf("image_size %q is not offered; choose one of %v", o.Size, config.OpenAIImageSizes())
	}
	if !slices.Contains(config.OpenAIImageQualities(), o.Quality) {
		return o, fmt.Errorf("image_quality %q is not offered; choose one of %v", o.Quality, config.OpenAIImageQualities())
	}
	return o, nil
}

func sizeForRatio(ratio string) string {
	switch ratio {
	case "3:2", "4:3", "16:9", "21:9":
		return "1536x1024"
	case "2:3", "3:4", "9:16":
		return "1024x1536"
	}
	return "1024x1024"
}

// openAICredits is the reservation for one OpenAI call. It equals what
// openai.ImagePlugin bills when OpenAI reports no usage (ReserveForCall), so
// the fallback never exceeds what the user was quoted.
func openAICredits(n int, quality string, refs int) int {
	cfg := openai.Config{ReserveUSD: config.OpenAIImageReserveUSD(), PerRefUSD: config.OpenAIImageReservePerRefUSD()}
	return creditsvc.CreditsFromYuan(cfg.ReserveForCall(n, quality, refs) * config.OpenAIUSDToCNY())
}

// imageSingleRefBound is an upper bound on the references prepareImageSingle
// sends, computable without the database.
func imageSingleRefBound(spec Spec) int {
	if spec.SourceImageAssetID != "" {
		return 1
	}
	return len(spec.Characters) * capability.CharacterMaxRefImages
}

// sequenceShotHasRef mirrors prepareImageSequence's choice of reference.
func sequenceShotHasRef(spec Spec, i int) bool {
	if spec.SourceImageAssetID != "" {
		return true
	}
	if i < len(spec.ShotSourceRefs) && spec.ShotSourceRefs[i] > 0 {
		return true
	}
	return i > 0 && spec.ImageSequenceMode == "continuity"
}
