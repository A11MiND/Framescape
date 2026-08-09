// Package id generates business-layer identifiers (ULID), used for every
// table's externally-exposed biz_id (PRD §9.2: "biz_id CHAR(26), ULID, 对外暴露").
// This is deliberately separate from Aether's own idgen.Generator, which
// mints RunIDs for WorkflowRun/TaskRun — the two ID spaces are owned by
// different layers (business vs. orchestration) and must not be conflated.
package id

import (
	"crypto/rand"
	"sync"

	"github.com/oklog/ulid/v2"
)

var (
	mu      sync.Mutex
	entropy = ulid.Monotonic(rand.Reader, 0)
)

// New returns a new ULID string, monotonically increasing within this process
// for identical millisecond timestamps.
func New() string {
	mu.Lock()
	defer mu.Unlock()
	return ulid.MustNew(ulid.Now(), entropy).String()
}
