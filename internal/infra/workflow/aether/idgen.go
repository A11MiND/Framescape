package aetherengine

import (
	"github.com/BabySid/aether/idgen"

	"aigc-platform/internal/pkg/id"
)

// ULIDGenerator implements idgen.Generator using our own ULID helper
// (internal/pkg/id), so Aether RunIDs and our business biz_ids come from the
// same well-tested ID scheme instead of introducing a second one.
type ULIDGenerator struct{}

func (ULIDGenerator) Generate(idgen.Context) string { return id.New() }

var _ idgen.Generator = ULIDGenerator{}
