package jobsvc

import (
	"testing"

	"aigc-platform/internal/domain/capability"
	"aigc-platform/internal/pkg/apperr"
)

func TestImageSequenceShotLimit(t *testing.T) {
	shots := make([]string, capability.ImageSequenceMaxShots)
	for i := range shots {
		shots[i] = "a harbour at dawn"
	}
	if _, err := EstimateCredits("image.sequence", Spec{Shots: shots}); err != nil {
		t.Fatalf("%d shots should be accepted: %v", len(shots), err)
	}
	_, err := EstimateCredits("image.sequence", Spec{Shots: append(shots, "one more")})
	if !hasCode(err, "shots_too_many") {
		t.Fatalf("one shot over the limit: got %v, want shots_too_many", err)
	}
	if _, err := EstimateCredits("image.sequence", Spec{}); !hasCode(err, "shots_required") {
		t.Fatalf("no shots: got %v, want shots_required", err)
	}
}

func hasCode(err error, code string) bool {
	e, ok := apperr.As(err)
	return ok && e.Code == code
}
