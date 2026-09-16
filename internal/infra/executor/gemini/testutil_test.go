package gemini

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/BabySid/aether/executor"
	"github.com/BabySid/aether/model"

	"aigc-platform/internal/infra/executor/assetstore"
)

// geminiServer stands in for Vertex AI's own generateContent endpoint —
// pointing a real *Client at an httptest.Server the same way
// minimax's testutil_test.go's jsonServer does, never hitting the real
// Gemini/Vertex AI API. Passing HTTPClient bypasses Application Default
// Credentials entirely (client.go's NewClient doc), so no real GCP
// credentials are needed for this to run.
func geminiServer(t *testing.T, handler http.HandlerFunc) (*Client, func()) {
	t.Helper()
	srv := httptest.NewServer(handler)
	client, err := NewClient(context.Background(), Config{
		ProjectID:  "test-project",
		Location:   "us-central1",
		HTTPClient: srv.Client(),
		BaseURL:    srv.URL,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return client, srv.Close
}

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

type fakeSink struct {
	mu           sync.Mutex
	nextID       int
	materialized []assetstore.NewAssetBytes
	bodies       [][]byte
}

func (s *fakeSink) Materialize(ctx context.Context, a assetstore.NewAsset) (string, error) {
	panic("fakeSink.Materialize not used by this package's executors")
}

func (s *fakeSink) MaterializeBytes(ctx context.Context, a assetstore.NewAssetBytes) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	body, _ := io.ReadAll(a.Body)
	s.bodies = append(s.bodies, body)
	a.Body = nil
	s.materialized = append(s.materialized, a)
	s.nextID++
	return fakeBizID(s.nextID), nil
}

func fakeBizID(n int) string { return "asset-" + string(rune('0'+n)) }

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
	return "", context.Canceled
}
