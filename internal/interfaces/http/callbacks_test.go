package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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

// TestHandleMiniMaxCallbackStatusPushDedup covers §11.3's "同一 task_id 的同一
// 状态重复推送要吃掉" — the second identical push must not re-publish to the
// video-wait channel, since minimax.VideoPlugin.wait() reacting twice to the
// same status transition is exactly the bug this dedup exists to prevent.
func TestHandleMiniMaxCallbackStatusPushDedup(t *testing.T) {
	s, _ := newFullTestServer(t)
	if s.redis == nil {
		t.Skip("no local Redis available, skipping dedup test")
	}
	r := s.Router()
	taskID := uniqueGoogleSub(t) // any short unique string stands in for a task id here

	sub := s.redis.Subscribe(context.Background(), "minimax:video:"+taskID)
	defer sub.Close()
	msgs := sub.Channel()

	push := `{"task":{"id":"` + taskID + `","status":"success"}}`
	rec := postCallback(t, r, "/internal/callbacks/minimax", push)
	if rec.Code != http.StatusOK {
		t.Fatalf("first push: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	rec = postCallback(t, r, "/internal/callbacks/minimax", push)
	if rec.Code != http.StatusOK {
		t.Fatalf("second (duplicate) push: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	select {
	case msg := <-msgs:
		if msg.Payload != "success" {
			t.Errorf("published payload = %q, want %q", msg.Payload, "success")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected exactly one publish from the first push, got none")
	}
	select {
	case msg := <-msgs:
		t.Errorf("got a second publish %+v — the duplicate push should have been deduped", msg)
	case <-time.After(300 * time.Millisecond):
		// expected: no second message
	}
}
