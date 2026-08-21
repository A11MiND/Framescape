package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"aigc-platform/internal/application/jobsvc"
	"aigc-platform/internal/application/projection"
)

// startSSE runs handleJobEvents in a goroutine against a cancellable
// context and returns the recorder plus a done channel — the handler
// blocks in an infinite select loop until either the context is cancelled
// or a terminal job_update event arrives, so it can never be driven
// through doJSON's synchronous helper.
func startSSE(s *Server, path, token string) (rec *httptest.ResponseRecorder, cancel context.CancelFunc, done <-chan struct{}) {
	ctx, cancelFn := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec = httptest.NewRecorder()
	d := make(chan struct{})
	go func() {
		s.Router().ServeHTTP(rec, req)
		close(d)
	}()
	return rec, cancelFn, d
}

func TestHandleJobEvents(t *testing.T) {
	s, _ := newFullTestServer(t)
	if s.redis == nil {
		t.Skip("no local Redis available, skipping SSE test")
	}
	r := s.Router()
	token, _ := registerAndFund(t, s, 1000)

	rec := doJSON(t, r, http.MethodPost, "/api/v1/jobs",
		createJobRequest{WorkflowName: "image.single", Spec: jobsvc.Spec{Text: "a cat"}}, token)
	var created map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	bizID := created["biz_id"].(string)
	runID := created["workflow_run_id"].(string)

	sseRec, cancel, done := startSSE(s, "/api/v1/jobs/"+bizID+"/events", token)

	// Give the handler time to Subscribe before publishing — a message
	// published before the subscription is live would simply be dropped,
	// same as any pub/sub system (no durable queue behind it).
	waitForSubscriber(t, s, projection.ChannelForRun(runID))

	ev := projection.Event{Type: "node_update", Node: "gen", Phase: "Running", WorkflowRunID: runID}
	payload, _ := json.Marshal(ev)
	if err := s.redis.Publish(context.Background(), projection.ChannelForRun(runID), payload).Err(); err != nil {
		t.Fatalf("publish: %v", err)
	}

	time.Sleep(150 * time.Millisecond)
	cancel()
	<-done

	body := sseRec.Body.String()
	if !strings.Contains(body, ": connected") {
		t.Errorf("SSE body missing the initial connected comment, got: %q", body)
	}
	if !strings.Contains(body, "event: node_update") {
		t.Errorf("SSE body missing node_update event, got: %q", body)
	}
	if !strings.Contains(body, `"phase":"Running"`) {
		t.Errorf("SSE body missing expected phase, got: %q", body)
	}
}

// TestHandleJobEventsTerminalCloses covers the handler's own auto-close
// behavior: a job_update carrying a terminal phase ends the stream with an
// explicit "done" event, with no need for the client to disconnect first.
func TestHandleJobEventsTerminalCloses(t *testing.T) {
	s, _ := newFullTestServer(t)
	if s.redis == nil {
		t.Skip("no local Redis available, skipping SSE test")
	}
	r := s.Router()
	token, _ := registerAndFund(t, s, 1000)

	rec := doJSON(t, r, http.MethodPost, "/api/v1/jobs",
		createJobRequest{WorkflowName: "image.single", Spec: jobsvc.Spec{Text: "a cat"}}, token)
	var created map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	bizID := created["biz_id"].(string)
	runID := created["workflow_run_id"].(string)

	sseRec, cancel, done := startSSE(s, "/api/v1/jobs/"+bizID+"/events", token)
	defer cancel() // no-op if the handler already returned on its own

	waitForSubscriber(t, s, projection.ChannelForRun(runID))

	ev := projection.Event{Type: "job_update", Phase: "Succeeded", WorkflowRunID: runID}
	payload, _ := json.Marshal(ev)
	if err := s.redis.Publish(context.Background(), projection.ChannelForRun(runID), payload).Err(); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not close on its own after a terminal job_update")
	}

	body := sseRec.Body.String()
	if !strings.Contains(body, "event: done") {
		t.Errorf("SSE body missing the closing done event, got: %q", body)
	}
}

func TestHandleJobEventsOwnershipBoundary(t *testing.T) {
	s, _ := newFullTestServer(t)
	if s.redis == nil {
		t.Skip("no local Redis available, skipping SSE test")
	}
	r := s.Router()
	tokenA, _ := registerAndFund(t, s, 1000)
	tokenB, _ := registerAndFund(t, s, 1000)

	rec := doJSON(t, r, http.MethodPost, "/api/v1/jobs",
		createJobRequest{WorkflowName: "image.single", Spec: jobsvc.Spec{Text: "a cat"}}, tokenA)
	var created map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	bizID := created["biz_id"].(string)

	// handleJobEvents' own jobsvc.Get call 404s before ever entering the
	// streaming loop, so this returns immediately — no goroutine needed.
	rec = doJSON(t, r, http.MethodGet, "/api/v1/jobs/"+bizID+"/events", nil, tokenB)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("other user's SSE: status = %d, want %d, body = %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

// waitForSubscriber polls PUBSUB NUMSUB rather than a fixed sleep, so this
// test isn't tuned to one machine's scheduling latency.
func waitForSubscriber(t *testing.T, s *Server, channel string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		n, err := s.redis.PubSubNumSub(context.Background(), channel).Result()
		if err == nil && n[channel] > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no subscriber ever appeared on channel %q", channel)
}
