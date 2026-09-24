package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func postCallback(t *testing.T, r http.Handler, path string, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestHandleMiniMaxCallbackTokenGate(t *testing.T) {
	t.Setenv("MINIMAX_CALLBACK_TOKEN", "shared-secret")
	s, _ := newFullTestServer(t)
	r := s.Router()

	rec := postCallback(t, r, "/internal/callbacks/minimax", `{}`)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no token: status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	rec = postCallback(t, r, "/internal/callbacks/minimax?token=wrong", `{}`)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong token: status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	rec = postCallback(t, r, "/internal/callbacks/minimax?token=shared-secret", `{}`)
	if rec.Code != http.StatusOK {
		t.Errorf("correct token: status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestHandleMiniMaxCallbackChallenge(t *testing.T) {
	s, _ := newFullTestServer(t)
	r := s.Router()

	rec := postCallback(t, r, "/internal/callbacks/minimax", `{"challenge":"hello-world"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Challenge string `json:"challenge"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, rec.Body.String())
	}
	if body.Challenge != "hello-world" {
		t.Errorf("challenge = %q, want %q", body.Challenge, "hello-world")
	}
}

// TestHandleMiniMaxCallbackUnrecognizedShape covers §11.3's "ack anyway so
// MiniMax doesn't retry-storm us" — a body that's neither a challenge nor a
// recognizable status push still gets a 200.
func TestHandleMiniMaxCallbackUnrecognizedShape(t *testing.T) {
	s, _ := newFullTestServer(t)
	r := s.Router()

	for _, body := range []string{`{}`, `{"unexpected":"shape"}`, `not even json`} {
		rec := postCallback(t, r, "/internal/callbacks/minimax", body)
		if rec.Code != http.StatusOK {
			t.Errorf("body %q: status = %d, want %d", body, rec.Code, http.StatusOK)
		}
	}
}

// TestMiniMaxCallbackNudge: a status push schedules an
// immediate poll of the node waiting on that remote task; the poll itself
// re-checks the provider, so duplicates are harmless.
func TestMiniMaxCallbackNudge(t *testing.T) {
	s, eng := newFullTestServer(t)
	r := s.Router()
	token, _ := registerAndFund(t, s, 1000)
	bizID := createJob(t, s, token)
	taskID := "cb-" + uniqueGoogleSub(t)
	if _, err := eng.db.Exec(`UPDATE job_nodes n JOIN jobs j ON j.id = n.job_id SET n.status = 'waiting', n.provider_task_id = ? WHERE j.biz_id = ?`, taskID, bizID); err != nil {
		t.Fatal(err)
	}
	rec := postCallback(t, r, "/internal/callbacks/minimax", `{"task":{"id":"`+taskID+`","status":"success"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("push: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if n := eng.nudges(); n != 1 {
		t.Fatalf("nudged %d polls, want 1", n)
	}
	rec = postCallback(t, r, "/internal/callbacks/minimax", `{"task":{"id":"unknown-task","status":"success"}}`)
	if rec.Code != http.StatusOK || eng.nudges() != 1 {
		t.Fatalf("unknown task: status %d, nudges %d", rec.Code, eng.nudges())
	}
}
