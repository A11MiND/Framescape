package minimax

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BabySid/aether/model"
)

func TestPromptEnhancePluginExecuteHappyPath(t *testing.T) {
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/v2/h3_context_ir", func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte(`{"task_id":"h3-1","base_resp":{"status_code":0}}`))
	})
	mux.HandleFunc("/v2/query/video_generation/h3-1", func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte(`{"task":{"id":"h3-1","status":"succeeded","content":{"prompt":"a richer prompt"},"usage":{"prompt_tokens":1000000,"completion_tokens":500000}}}`))
	})
	client := NewClient(srv.URL, "test-api-key")

	plugin := NewPromptEnhancePlugin(client, newFakeReader(), newFakeFileCache())
	req := execRequest(t, "task-1", map[string]any{"prompt": "a cat", "duration": "5", "ratio": "16:9"})

	out, err := plugin.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Code != 0 {
		t.Fatalf("Code = %d, Message = %q, want success", out.Code, out.Message)
	}
	if got := outputValue[string](t, out, "enhanced-prompt"); got != "a richer prompt" {
		t.Errorf("enhanced-prompt = %q, want %q", got, "a richer prompt")
	}
	wantCost := 1.0*promptEnhanceInputYuanPerM + 0.5*promptEnhanceOutputYuanPerM
	if got := outputValue[float64](t, out, "cost-yuan"); got != wantCost {
		t.Errorf("cost-yuan = %v, want %v (1M input + 0.5M output tokens at §3.4's rates)", got, wantCost)
	}
}

func TestPromptEnhancePluginExecuteFailedStatus(t *testing.T) {
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/v2/h3_context_ir", func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte(`{"task_id":"h3-2","base_resp":{"status_code":0}}`))
	})
	mux.HandleFunc("/v2/query/video_generation/h3-2", func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte(`{"task":{"id":"h3-2","status":"failed","error":{"code":"internal","message":"oops"}}}`))
	})
	client := NewClient(srv.URL, "test-api-key")

	plugin := NewPromptEnhancePlugin(client, newFakeReader(), newFakeFileCache())
	req := execRequest(t, "task-1", map[string]any{"prompt": "x", "duration": "5", "ratio": "16:9"})
	out, err := plugin.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Code != model.ExecCodeFailed {
		t.Errorf("Code = %d, want ExecCodeFailed", out.Code)
	}
}

// TestPromptEnhancePluginExecuteMutualExclusion confirms this plugin reuses
// buildContent's mode/ratio validation verbatim (prompt_enhance.go's own
// doc) — it must reject the same first_frame+reference combination
// minimax.video does, never reaching the network at all.
func TestPromptEnhancePluginExecuteMutualExclusion(t *testing.T) {
	client, closeFn := jsonServer(t, func(w http.ResponseWriter, req *http.Request) {
		t.Fatal("CreateH3ContextIRTask must never be called when buildContent already rejected the request")
	})
	defer closeFn()

	plugin := NewPromptEnhancePlugin(client, newFakeReader(), newFakeFileCache())
	req := execRequest(t, "task-1", map[string]any{
		"prompt": "x", "duration": "5", "ratio": "16:9",
		"first-frame-asset-id":      "a",
		"reference-image-asset-ids": []string{"b"},
	})
	out, err := plugin.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Code != model.ExecCodeFailed {
		t.Errorf("Code = %d, want ExecCodeFailed (mutual_exclusion)", out.Code)
	}
}
