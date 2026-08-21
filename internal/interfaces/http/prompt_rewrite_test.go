package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aigc-platform/internal/infra/executor/minimax"
	"aigc-platform/internal/infra/persistence"
	"aigc-platform/internal/pkg/config"
)

// newPromptRewriteTestServer wires only what handleRewritePrompt touches:
// db (requireAuth/registerAndFund need it, the handler itself doesn't) and
// a *minimax.Client pointed at a local fake chat/completions server instead
// of the real MiniMax API — same convention trial_test.go established.
// Unlike trial.go this handler never touches s.redis, so no Redis
// dependency/skip is needed here.
func newPromptRewriteTestServer(t *testing.T, minimaxBaseURL string) *Server {
	t.Helper()
	db, err := persistence.Open(persistence.Config{DSN: config.MySQLDSN()})
	if err != nil {
		t.Skipf("no local MySQL available, skipping: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	if err := sqlDB.Ping(); err != nil {
		t.Skipf("no local MySQL available, skipping: %v", err)
	}
	mm := minimax.NewClient(minimaxBaseURL, "test-api-key")
	return NewServer(db, nil, nil, nil, nil, testJWTSecret, mm, nil)
}

func chatCompletionResponse(content string) string {
	b, _ := json.Marshal(map[string]any{
		"id": "test-id",
		"choices": []map[string]any{
			{"message": map[string]any{"role": "assistant", "content": content}, "finish_reason": "stop"},
		},
	})
	return string(b)
}

func TestHandleRewritePromptHappyPath(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte(chatCompletionResponse(`"一只戴着帽子的猫，柔和的午后阳光"`)))
	}))
	defer fake.Close()
	s := newPromptRewriteTestServer(t, fake.URL)
	r := s.Router()
	token, _ := registerAndFund(t, s, 0)

	rec := doJSON(t, r, http.MethodPost, "/api/v1/prompts/rewrite", struct {
		Text string `json:"text"`
	}{Text: "a cat with a hat"}, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// The surrounding quotes MiniMax sometimes wraps its answer in must be
	// stripped — handleRewritePrompt's own strings.Trim call.
	if strings.HasPrefix(body.Text, `"`) || strings.HasSuffix(body.Text, `"`) {
		t.Errorf("text = %q, want surrounding quotes stripped", body.Text)
	}
	if body.Text != "一只戴着帽子的猫，柔和的午后阳光" {
		t.Errorf("text = %q, want the fake model's (unquoted) answer", body.Text)
	}
}

func TestHandleRewritePromptMissingText(t *testing.T) {
	s := newPromptRewriteTestServer(t, "http://unused.invalid")
	r := s.Router()
	token, _ := registerAndFund(t, s, 0)

	for _, text := range []string{"", "   "} {
		rec := doJSON(t, r, http.MethodPost, "/api/v1/prompts/rewrite", struct {
			Text string `json:"text"`
		}{Text: text}, token)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("text=%q: status = %d, want %d", text, rec.Code, http.StatusBadRequest)
		}
	}
}

// TestHandleRewritePromptTruncatesLongText confirms the 1500-rune cap is
// actually applied to what gets sent upstream, not just to some local echo
// — captured by inspecting the fake server's received request body.
func TestHandleRewritePromptTruncatesLongText(t *testing.T) {
	var receivedContent string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(req.Body).Decode(&body)
		if len(body.Messages) > 0 {
			receivedContent = body.Messages[0].Content
		}
		w.Write([]byte(chatCompletionResponse("ok")))
	}))
	defer fake.Close()
	s := newPromptRewriteTestServer(t, fake.URL)
	r := s.Router()
	token, _ := registerAndFund(t, s, 0)

	longText := strings.Repeat("a", 2000)
	rec := doJSON(t, r, http.MethodPost, "/api/v1/prompts/rewrite", struct {
		Text string `json:"text"`
	}{Text: longText}, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	idx := strings.Index(receivedContent, "原始描述：")
	if idx == -1 {
		t.Fatalf("upstream request never carried the expected instruction marker, got: %q", receivedContent)
	}
	sentText := receivedContent[idx+len("原始描述："):]
	if got := len([]rune(sentText)); got != 1500 {
		t.Errorf("upstream received %d runes of user text, want exactly 1500 (the truncation cap)", got)
	}
}

func TestHandleRewritePromptUpstreamError(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer fake.Close()
	s := newPromptRewriteTestServer(t, fake.URL)
	r := s.Router()
	token, _ := registerAndFund(t, s, 0)

	rec := doJSON(t, r, http.MethodPost, "/api/v1/prompts/rewrite", struct {
		Text string `json:"text"`
	}{Text: "a cat"}, token)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusBadGateway, rec.Body.String())
	}
}

func TestHandleRewritePromptEmptyChoices(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte(`{"id":"test-id","choices":[]}`))
	}))
	defer fake.Close()
	s := newPromptRewriteTestServer(t, fake.URL)
	r := s.Router()
	token, _ := registerAndFund(t, s, 0)

	rec := doJSON(t, r, http.MethodPost, "/api/v1/prompts/rewrite", struct {
		Text string `json:"text"`
	}{Text: "a cat"}, token)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusUnprocessableEntity, rec.Body.String())
	}
}

func TestHandleRewritePromptBlankContent(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte(chatCompletionResponse("   ")))
	}))
	defer fake.Close()
	s := newPromptRewriteTestServer(t, fake.URL)
	r := s.Router()
	token, _ := registerAndFund(t, s, 0)

	rec := doJSON(t, r, http.MethodPost, "/api/v1/prompts/rewrite", struct {
		Text string `json:"text"`
	}{Text: "a cat"}, token)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusUnprocessableEntity, rec.Body.String())
	}
}
