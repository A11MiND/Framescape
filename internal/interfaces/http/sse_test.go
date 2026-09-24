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
	"aigc-platform/internal/infra/orchestrator"
	"aigc-platform/internal/infra/realtime"
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

func streamServer(t *testing.T) (*Server, *orchestrator.RedisPublisher) {
	t.Helper()
	s, _ := newFullTestServer(t)
	if s.redis == nil {
		t.Skip("no local Redis available, skipping SSE test")
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s.hub = realtime.NewHub(ctx, s.redis)
	return s, orchestrator.NewRedisPublisher(s.redis)
}

func createJob(t *testing.T, s *Server, token string) string {
	t.Helper()
	rec := doJSON(t, s.Router(), http.MethodPost, "/api/v1/jobs",
		createJobRequest{WorkflowName: "image.single", Spec: jobsvc.Spec{Text: "a cat"}}, token)
	var created map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	bizID, _ := created["biz_id"].(string)
	if bizID == "" {
		t.Fatalf("create job: %s", rec.Body.String())
	}
	return bizID
}

func publish(t *testing.T, pub *orchestrator.RedisPublisher, userID uint64, bizID, typ string, payload map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(payload)
	if err := pub.PublishEvents(context.Background(), []orchestrator.Event{{ID: 1 << 40, UserID: userID, JobBizID: bizID, Type: typ, Payload: raw}}); err != nil {
		t.Fatalf("publish: %v", err)
	}
}

func TestHandleJobEvents(t *testing.T) {
	s, pub := streamServer(t)
	token, uid := registerAndFund(t, s, 1000)
	bizID := createJob(t, s, token)

	sseRec, cancel, done := startSSE(s, "/api/v1/jobs/"+bizID+"/events", token)
	waitForSubscriber(t, s, orchestrator.UserChannel(uid))
	publish(t, pub, uid, "someone-else", orchestrator.EventNodeStatus, map[string]any{"node": "x", "status": "running"})
	publish(t, pub, uid, bizID, orchestrator.EventNodeStatus, map[string]any{"node": "gen", "status": "running"})
	time.Sleep(150 * time.Millisecond)
	cancel()
	<-done

	body := sseRec.Body.String()
	if !strings.Contains(body, ": connected") || !strings.Contains(body, "event: node_update") || !strings.Contains(body, `"phase":"Running"`) {
		t.Errorf("unexpected SSE body: %q", body)
	}
	if strings.Contains(body, `"node":"x"`) {
		t.Errorf("another job's event leaked into this stream: %q", body)
	}
}

func TestHandleJobEventsTerminalCloses(t *testing.T) {
	s, pub := streamServer(t)
	token, uid := registerAndFund(t, s, 1000)
	bizID := createJob(t, s, token)

	sseRec, cancel, done := startSSE(s, "/api/v1/jobs/"+bizID+"/events", token)
	defer cancel()
	waitForSubscriber(t, s, orchestrator.UserChannel(uid))
	publish(t, pub, uid, bizID, orchestrator.EventJobFinished, map[string]any{"status": "succeeded"})

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not close after the job finished")
	}
	if body := sseRec.Body.String(); !strings.Contains(body, "event: done") {
		t.Errorf("SSE body missing the done event: %q", body)
	}
}

func TestHandleJobEventsOwnershipBoundary(t *testing.T) {
	s, _ := streamServer(t)
	tokenA, _ := registerAndFund(t, s, 1000)
	tokenB, _ := registerAndFund(t, s, 1000)
	bizID := createJob(t, s, tokenA)
	rec := doJSON(t, s.Router(), http.MethodGet, "/api/v1/jobs/"+bizID+"/events", nil, tokenB)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("other user's SSE: status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

// TestHandleStreamReplaysThenStreamsLive: a client resuming from event 0
// first gets the job.created event stored at submission, then live events.
func TestHandleStreamReplaysThenStreamsLive(t *testing.T) {
	s, pub := streamServer(t)
	token, uid := registerAndFund(t, s, 1000)
	bizID := createJob(t, s, token)

	sseRec, cancel, done := startSSE(s, "/api/v1/stream?last_event_id=0", token)
	waitForSubscriber(t, s, orchestrator.UserChannel(uid))
	publish(t, pub, uid, bizID, orchestrator.EventNeedsReview, map[string]any{})
	time.Sleep(150 * time.Millisecond)
	cancel()
	<-done

	body := sseRec.Body.String()
	created := strings.Index(body, "event: job.created")
	live := strings.Index(body, "event: job.needs_review")
	if created < 0 || live < created || !strings.Contains(body, "id: ") {
		t.Fatalf("expected replayed job.created then live needs_review, got %q", body)
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
