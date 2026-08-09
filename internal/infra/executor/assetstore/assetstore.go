// Package assetstore defines the narrow write capability executors need to
// turn a generated file into a durable, citable asset (PRD's "materialize"
// step, §10.2/§10.3: provider URLs expire in 24h and must be persisted
// immediately). It is intentionally a tiny interface — not a repository, not
// a service — so mock/minimax/ffmpeg executors depend on exactly one method
// and stay testable without a real database.
package assetstore

import (
	"context"
	"io"
)

// NewAsset describes one materialized generation output whose bytes already
// live somewhere citable (used by mock executors, which embed a data URI
// directly — nothing to upload).
type NewAsset struct {
	UserID        uint64
	ProjectID     *uint64
	Type          string // image/video/audio
	Source        string // generated/derived
	FromTaskRunID string
	StorageKey    string
	PublicURL     string
	Mime          string
	Width         int
	Height        int
	DurationMs    int
	SizeBytes     int64
	ResolutionTag string // "" / 768P / 2K
	Meta          map[string]any
}

// NewAssetBytes describes a generation output whose bytes the executor is
// handing over directly (real provider responses: MiniMax's URLs expire in
// 24h — PRD R3 — so the executor downloads them and hands the bytes here to
// be re-uploaded to our own storage before the row is written).
type NewAssetBytes struct {
	UserID        uint64
	ProjectID     *uint64
	Type          string
	Source        string
	FromTaskRunID string
	Body          io.Reader
	SizeBytes     int64
	Ext           string // "jpg", "png", ... — used to build the storage key
	Mime          string
	Width         int
	Height        int
	DurationMs    int
	ResolutionTag string
	Meta          map[string]any
}

// Sink persists a generation output and returns its externally-visible biz_id.
type Sink interface {
	Materialize(ctx context.Context, a NewAsset) (bizID string, err error)
	MaterializeBytes(ctx context.Context, a NewAssetBytes) (bizID string, err error)
}

// Reader looks up an already-materialized asset's public URL — needed by
// executors that consume other assets as input (local.compose reading the
// four panel images to tile them; later, minimax.video reading a reference
// image). Kept separate from Sink because most executors only ever write.
type Reader interface {
	PublicURL(ctx context.Context, bizID string) (string, error)
}
