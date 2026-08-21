package minimax

import (
	"context"
	"net/http"
	"testing"
)

func TestGenerateImage(t *testing.T) {
	var gotAuth, gotPath string
	client, closeFn := jsonServer(t, func(w http.ResponseWriter, req *http.Request) {
		gotAuth = req.Header.Get("Authorization")
		gotPath = req.URL.Path
		w.Write([]byte(`{"id":"img-1","data":{"image_urls":["https://example.test/a.png"]},"base_resp":{"status_code":0}}`))
	})
	defer closeFn()

	resp, err := client.GenerateImage(context.Background(), ImageGenerationRequest{Model: "image-01", Prompt: "a cat", N: 1})
	if err != nil {
		t.Fatalf("GenerateImage: %v", err)
	}
	if gotPath != "/v1/image_generation" {
		t.Errorf("path = %q, want /v1/image_generation", gotPath)
	}
	if gotAuth != "Bearer test-api-key" {
		t.Errorf("Authorization = %q, want Bearer test-api-key", gotAuth)
	}
	if resp.ID != "img-1" || len(resp.Data.ImageURLs) != 1 {
		t.Errorf("resp = %+v, want id=img-1 with 1 image url", resp)
	}
}

// TestGenerateImageBusinessError confirms client.go never interprets
// base_resp.status_code itself (PRD §3.1: HTTP is always 200 even on
// business errors) — that classification is image.go's job, this layer
// just hands the whole response back unexamined on a 200.
func TestGenerateImageBusinessError(t *testing.T) {
	client, closeFn := jsonServer(t, func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte(`{"id":"img-2","base_resp":{"status_code":1026,"status_msg":"sensitive"}}`))
	})
	defer closeFn()

	resp, err := client.GenerateImage(context.Background(), ImageGenerationRequest{Model: "image-01", Prompt: "x"})
	if err != nil {
		t.Fatalf("GenerateImage: %v", err)
	}
	if resp.BaseResp.StatusCode != 1026 {
		t.Errorf("BaseResp.StatusCode = %d, want 1026 passed through unexamined", resp.BaseResp.StatusCode)
	}
}

func TestGenerateImageHTTPError(t *testing.T) {
	client, closeFn := jsonServer(t, func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte("upstream down"))
	})
	defer closeFn()

	if _, err := client.GenerateImage(context.Background(), ImageGenerationRequest{Model: "image-01", Prompt: "x"}); err == nil {
		t.Fatal("expected an error for HTTP 502")
	}
}

func TestUploadFile(t *testing.T) {
	var gotPurpose, gotFilename string
	client, closeFn := jsonServer(t, func(w http.ResponseWriter, req *http.Request) {
		if err := req.ParseMultipartForm(10 << 20); err != nil {
			t.Fatalf("parse multipart: %v", err)
		}
		gotPurpose = req.FormValue("purpose")
		if fh := req.MultipartForm.File["file"]; len(fh) == 1 {
			gotFilename = fh[0].Filename
		}
		w.Write([]byte(`{"file":{"file_id":42,"bytes":3,"filename":"a.jpg","purpose":"video_generation_input"},"base_resp":{"status_code":0}}`))
	})
	defer closeFn()

	resp, err := client.UploadFile(context.Background(), "video_generation_input", "a.jpg", []byte{1, 2, 3})
	if err != nil {
		t.Fatalf("UploadFile: %v", err)
	}
	if gotPurpose != "video_generation_input" {
		t.Errorf("purpose field = %q, want video_generation_input", gotPurpose)
	}
	if gotFilename != "a.jpg" {
		t.Errorf("filename = %q, want a.jpg", gotFilename)
	}
	if resp.File.FileID != 42 {
		t.Errorf("file_id = %d, want 42", resp.File.FileID)
	}
}

func TestDownloadImage(t *testing.T) {
	client, closeFn := jsonServer(t, func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte("fake-png-bytes"))
	})
	defer closeFn()

	data, contentType, err := client.DownloadImage(context.Background(), client.baseURL+"/whatever.png")
	if err != nil {
		t.Fatalf("DownloadImage: %v", err)
	}
	if string(data) != "fake-png-bytes" {
		t.Errorf("data = %q, want fake-png-bytes", data)
	}
	if contentType != "image/png" {
		t.Errorf("contentType = %q, want image/png", contentType)
	}
}

func TestDownloadImageMissingContentTypeDefaultsToJPEG(t *testing.T) {
	client, closeFn := jsonServer(t, func(w http.ResponseWriter, req *http.Request) {
		// An explicit empty value, not just an unset header — net/http's
		// server auto-sniffs and fills in Content-Type on the first Write
		// whenever a handler leaves it genuinely unset, so this is the only
		// way to actually produce an empty header through a real HTTP
		// response and exercise DownloadImage's fallback for real.
		w.Header().Set("Content-Type", "")
		w.Write([]byte("bytes"))
	})
	defer closeFn()

	_, contentType, err := client.DownloadImage(context.Background(), client.baseURL+"/x")
	if err != nil {
		t.Fatalf("DownloadImage: %v", err)
	}
	if contentType != "image/jpeg" {
		t.Errorf("contentType = %q, want the image/jpeg fallback", contentType)
	}
}

func TestDownloadImageHTTPError(t *testing.T) {
	client, closeFn := jsonServer(t, func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	defer closeFn()

	if _, _, err := client.DownloadImage(context.Background(), client.baseURL+"/gone"); err == nil {
		t.Fatal("expected an error for HTTP 404")
	}
}
