package minimax

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

func TestCreateVideoTask(t *testing.T) {
	client, closeFn := jsonServer(t, func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v2/video_generation" {
			t.Errorf("path = %q, want /v2/video_generation", req.URL.Path)
		}
		w.Write([]byte(`{"task_id":"t-1","base_resp":{"status_code":0}}`))
	})
	defer closeFn()

	resp, err := client.CreateVideoTask(context.Background(), VideoGenerationRequest{
		Model: videoModel, Content: []VideoContentItem{{Type: "text", Text: "a cat"}}, Resolution: "768P", Duration: 5,
	})
	if err != nil {
		t.Fatalf("CreateVideoTask: %v", err)
	}
	if resp.TaskID != "t-1" {
		t.Errorf("task_id = %q, want t-1", resp.TaskID)
	}
}

func TestCreateVideoTaskHTTPError(t *testing.T) {
	client, closeFn := jsonServer(t, func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte("insufficient balance"))
	})
	defer closeFn()

	_, err := client.CreateVideoTask(context.Background(), VideoGenerationRequest{})
	var httpErr *HTTPStatusError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusPaymentRequired {
		t.Fatalf("err = %v, want *HTTPStatusError{402}", err)
	}
}

func TestQueryVideoTask(t *testing.T) {
	client, closeFn := jsonServer(t, func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v2/query/video_generation/t-1" {
			t.Errorf("path = %q, want /v2/query/video_generation/t-1", req.URL.Path)
		}
		w.Write([]byte(`{"task":{"id":"t-1","status":"succeeded","content":{"url":"https://example.test/v.mp4"},"resolution":"768P","duration":5}}`))
	})
	defer closeFn()

	status, err := client.QueryVideoTask(context.Background(), "t-1")
	if err != nil {
		t.Fatalf("QueryVideoTask: %v", err)
	}
	if status.Task.Status != "succeeded" || status.Task.Content == nil || status.Task.Content.URL != "https://example.test/v.mp4" {
		t.Errorf("status = %+v, want succeeded with a content URL", status.Task)
	}
}

func TestDownloadVideo(t *testing.T) {
	client, closeFn := jsonServer(t, func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte("fake-mp4-bytes"))
	})
	defer closeFn()

	data, err := client.DownloadVideo(context.Background(), client.baseURL+"/v.mp4")
	if err != nil {
		t.Fatalf("DownloadVideo: %v", err)
	}
	if string(data) != "fake-mp4-bytes" {
		t.Errorf("data = %q, want fake-mp4-bytes", data)
	}
}
