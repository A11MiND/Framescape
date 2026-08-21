package minimax

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/BabySid/aether/executor"
	"github.com/BabySid/aether/model"

	"aigc-platform/internal/infra/executor/assetstore"
)

// decodeJSON reads a fake server's incoming request body into dst — shared
// by every test that needs to assert on what an executor actually sent
// upstream, not just how it reacted to the canned response.
func decodeJSON(req *http.Request, dst any) error {
	return json.NewDecoder(req.Body).Decode(dst)
}

// execRequest builds a *executor.ExecuteRequest whose Inputs carry one
// model.Parameter per entry in params, JSON-marshaled — matching exactly
// what executor.BindInputs expects (model.Parameter.Value is already
// json.RawMessage, see aether's own bind.go doc). Every plugin's Execute in
// this package starts by binding its config struct this same way, so this
// is the one builder every executor test in this package shares.
func execRequest(t *testing.T, taskRunID string, params map[string]any) *executor.ExecuteRequest {
	t.Helper()
	ps := make([]model.Parameter, 0, len(params))
	for name, v := range params {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal param %q: %v", name, err)
		}
		ps = append(ps, model.Parameter{Name: name, Value: raw})
	}
	return &executor.ExecuteRequest{
		TaskRunID: taskRunID,
		Inputs:    &model.Inputs{Parameters: ps},
	}
}

// outputValue decodes one named parameter out of a successful ExecOutputs —
// every plugin here builds its output via executor.OutputFrom, which is
// this struct's exact mirror image.
func outputValue[T any](t *testing.T, out *model.ExecOutputs, name string) T {
	t.Helper()
	for _, p := range out.Parameters {
		if p.Name == name {
			var v T
			if err := json.Unmarshal(p.Value, &v); err != nil {
				t.Fatalf("decode output %q: %v", name, err)
			}
			return v
		}
	}
	t.Fatalf("output parameter %q not found in %+v", name, out.Parameters)
	var zero T
	return zero
}

// fakeSink is an in-memory assetstore.Sink — every MaterializeBytes call is
// recorded (including the body bytes actually read off, since a caller
// passing an io.Reader whose bytes never get consumed would be a real bug)
// so tests can assert on what an executor actually tried to persist.
type fakeSink struct {
	mu           sync.Mutex
	nextID       int
	materialized []assetstore.NewAssetBytes
	bodies       [][]byte
	// failN, if > 0, makes the Nth call (1-indexed) return an error instead
	// of succeeding — for exercising a partial-materialize-failure path.
	failN int
	calls int
}

func (s *fakeSink) Materialize(ctx context.Context, a assetstore.NewAsset) (string, error) {
	panic("fakeSink.Materialize not used by this package's executors")
}

func (s *fakeSink) MaterializeBytes(ctx context.Context, a assetstore.NewAssetBytes) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.failN > 0 && s.calls == s.failN {
		return "", context.DeadlineExceeded
	}
	body, _ := io.ReadAll(a.Body)
	s.bodies = append(s.bodies, body)
	a.Body = nil
	s.materialized = append(s.materialized, a)
	s.nextID++
	return fakeBizID(s.nextID), nil
}

func fakeBizID(n int) string { return fmt.Sprintf("asset-%d", n) }

// fakeReader is an in-memory assetstore.Reader — maps a biz_id to a URL a
// test's own httptest.Server serves real bytes from.
type fakeReader struct {
	mu   sync.Mutex
	urls map[string]string
}

func newFakeReader() *fakeReader { return &fakeReader{urls: map[string]string{}} }

func (r *fakeReader) set(bizID, url string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.urls[bizID] = url
}

func (r *fakeReader) PublicURL(ctx context.Context, bizID string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if u, ok := r.urls[bizID]; ok {
		return u, nil
	}
	return "", context.Canceled // any non-nil error — "asset not found"-shaped for this test double
}

// fakeFileCache is an in-memory FileCache.
type fakeFileCache struct {
	mu    sync.Mutex
	byID  map[string]string
	calls int // Put call count, for asserting cache-miss-then-populate happened exactly once
}

func newFakeFileCache() *fakeFileCache { return &fakeFileCache{byID: map[string]string{}} }

func (c *fakeFileCache) Get(ctx context.Context, assetBizID string) (string, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	id, ok := c.byID[assetBizID]
	return id, ok, nil
}

func (c *fakeFileCache) Put(ctx context.Context, assetBizID, fileID, purpose string, expireAt time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.byID[assetBizID] = fileID
	c.calls++
	return nil
}

// jsonServer stands in for MiniMax itself: handler decides the response per
// request, so tests can express "fails once, then succeeds", "returns
// terminal status on the very first poll", etc. directly. Never hits the
// real MiniMax API — this codebase's own established testing convention.
func jsonServer(t *testing.T, handler http.HandlerFunc) (*Client, func()) {
	t.Helper()
	srv := httptest.NewServer(handler)
	return NewClient(srv.URL, "test-api-key"), srv.Close
}
