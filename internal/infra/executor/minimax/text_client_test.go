package minimax

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

func TestChatCompletion(t *testing.T) {
	client, closeFn := jsonServer(t, func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %q, want /v1/chat/completions", req.URL.Path)
		}
		w.Write([]byte(`{"id":"c-1","choices":[{"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}]}`))
	})
	defer closeFn()

	resp, err := client.ChatCompletion(context.Background(), ChatCompletionRequest{
		Model:    "MiniMax-M3",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if len(resp.Choices) != 1 || resp.Choices[0].Message.Content != "hello" {
		t.Errorf("resp = %+v, want one choice with content=hello", resp)
	}
}

// TestChatCompletionHTTPError confirms this endpoint's errors are real HTTP
// status codes (unlike image_generation's body-embedded base_resp) —
// text_client.go's own doc on why classifyVideoError is reused for it.
func TestChatCompletionHTTPError(t *testing.T) {
	client, closeFn := jsonServer(t, func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte("rate limited"))
	})
	defer closeFn()

	_, err := client.ChatCompletion(context.Background(), ChatCompletionRequest{Model: "MiniMax-M3"})
	var httpErr *HTTPStatusError
	if !errors.As(err, &httpErr) {
		t.Fatalf("err = %v, want an *HTTPStatusError", err)
	}
	if httpErr.StatusCode != http.StatusTooManyRequests {
		t.Errorf("StatusCode = %d, want %d", httpErr.StatusCode, http.StatusTooManyRequests)
	}
}
