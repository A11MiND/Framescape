package executor

import "context"

// Attribution identifies who and what an outbound provider call is for, so
// transport-level recording can charge it to a job node.
type Attribution struct {
	UserID  uint64
	JobID   uint64
	NodeID  uint64
	Attempt int
}

type attributionKey struct{}

// WithAttribution returns ctx carrying a.
func WithAttribution(ctx context.Context, a Attribution) context.Context {
	return context.WithValue(ctx, attributionKey{}, a)
}

// AttributionFrom returns the attribution in ctx, if any.
func AttributionFrom(ctx context.Context) (Attribution, bool) {
	a, ok := ctx.Value(attributionKey{}).(Attribution)
	return a, ok
}
