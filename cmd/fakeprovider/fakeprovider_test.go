package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"

	"aigc-platform/internal/infra/executor/minimax"
)

func newTestServer(t *testing.T, b behavior) (*server, *httptest.Server) {
	t.Helper()
	cfg := &config{Seed: 42}
	cfg.set(b)
	srv := newServer(cfg)
	ts := httptest.NewServer(srv.routes())
	cfg.PublicURL = ts.URL
	t.Cleanup(func() {
		ts.Close()
		srv.close()
	})
	return srv, ts
}

func TestMiniMaxImageThroughRealClient(t *testing.T) {
	_, ts := newTestServer(t, behavior{})
	client := minimax.NewClient(ts.URL, "test-key")

	resp, err := client.GenerateImage(context.Background(), minimax.ImageGenerationRequest{Model: "image-01", Prompt: "a red tram", N: 2, AspectRatio: "16:9"})
	if err != nil {
		t.Fatalf("GenerateImage: %v", err)
	}
	if resp.BaseResp.StatusCode != 0 || len(resp.Data.ImageURLs) != 2 || resp.Metadata.SuccessCount != "2" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	data, mime, err := client.DownloadImage(context.Background(), resp.Data.ImageURLs[0])
	if err != nil || mime != "image/png" || len(data) == 0 {
		t.Fatalf("DownloadImage: mime=%q len=%d err=%v", mime, len(data), err)
	}
}

func TestMiniMaxImageRateLimitUsesBusinessCode(t *testing.T) {
	_, ts := newTestServer(t, behavior{RateLimitRate: 1})
	client := minimax.NewClient(ts.URL, "test-key")
	resp, err := client.GenerateImage(context.Background(), minimax.ImageGenerationRequest{Prompt: "x", N: 1})
	if err != nil {
		t.Fatalf("GenerateImage transport error: %v", err)
	}
	if resp.BaseResp.StatusCode != 1002 {
		t.Fatalf("status_code = %d, want 1002", resp.BaseResp.StatusCode)
	}
}

func TestMiniMaxVideoLifecycle(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	srv, ts := newTestServer(t, behavior{VideoLatency: latencyRange{Min: 200 * time.Millisecond, Max: 200 * time.Millisecond}})
	client := minimax.NewClient(ts.URL, "test-key")
	ctx := context.Background()

	created, err := client.CreateVideoTask(ctx, minimax.VideoGenerationRequest{Model: "MiniMax-H3", Resolution: "768P", Duration: 4, Ratio: "16:9"})
	if err != nil || created.TaskID == "" {
		t.Fatalf("CreateVideoTask: %+v err=%v", created, err)
	}
	first, err := client.QueryVideoTask(ctx, created.TaskID)
	if err != nil {
		t.Fatalf("QueryVideoTask: %v", err)
	}
	if first.Task.Status != "queued" && first.Task.Status != "running" {
		t.Fatalf("early status = %q", first.Task.Status)
	}

	time.Sleep(250 * time.Millisecond)
	done, err := client.QueryVideoTask(ctx, created.TaskID)
	if err != nil {
		t.Fatalf("QueryVideoTask: %v", err)
	}
	if done.Task.Status != "succeeded" || done.Task.Content == nil || done.Task.Usage == nil || done.Task.Usage.TotalSeconds != 4 {
		t.Fatalf("final status: %+v", done.Task)
	}
	video, err := client.DownloadVideo(ctx, done.Task.Content.URL)
	if err != nil || len(video) == 0 {
		t.Fatalf("DownloadVideo: len=%d err=%v", len(video), err)
	}

	stats := fetchStats(t, ts.URL)
	if got := stats.Endpoints["minimax.video_generation"].Calls; got != 1 {
		t.Fatalf("video_generation calls = %d, want 1", got)
	}
	_ = srv
}

func TestVideoRateLimitIsHTTP429(t *testing.T) {
	_, ts := newTestServer(t, behavior{RateLimitRate: 1})
	client := minimax.NewClient(ts.URL, "test-key")
	_, err := client.CreateVideoTask(context.Background(), minimax.VideoGenerationRequest{Resolution: "768P", Duration: 4})
	var statusErr *minimax.HTTPStatusError
	if err == nil || !asHTTPStatus(err, &statusErr) || statusErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("err = %v, want HTTP 429", err)
	}
}

func TestOpenAIGenerationsReturnsUsage(t *testing.T) {
	_, ts := newTestServer(t, behavior{})
	body := strings.NewReader(`{"model":"gpt-image","prompt":"a comic page","n":1,"size":"1536x1024"}`)
	resp, err := http.Post(ts.URL+"/v1/images/generations", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Data []struct {
			B64 string `json:"b64_json"`
		} `json:"data"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || len(out.Data) != 1 || out.Data[0].B64 == "" || out.Usage.OutputTokens == 0 {
		t.Fatalf("unexpected response: status=%d %+v", resp.StatusCode, out)
	}
}

func TestRuntimeConfigUpdate(t *testing.T) {
	_, ts := newTestServer(t, behavior{})
	resp, err := http.Post(ts.URL+"/_fake/config", "application/json", strings.NewReader(`{"error_rate":1,"image_latency":"0s"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	client := minimax.NewClient(ts.URL, "test-key")
	out, err := client.GenerateImage(context.Background(), minimax.ImageGenerationRequest{Prompt: "x"})
	if err != nil || out.BaseResp.StatusCode == 0 {
		t.Fatalf("expected injected failure, got %+v err=%v", out, err)
	}
}

type statsResponse struct {
	Endpoints map[string]endpointStats `json:"endpoints"`
}

func fetchStats(t *testing.T, base string) statsResponse {
	t.Helper()
	resp, err := http.Get(base + "/_fake/stats")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out statsResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func asHTTPStatus(err error, target **minimax.HTTPStatusError) bool {
	e, ok := err.(*minimax.HTTPStatusError)
	if ok {
		*target = e
	}
	return ok
}
