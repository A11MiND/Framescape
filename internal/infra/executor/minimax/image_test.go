package minimax

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BabySid/aether/model"
)

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.White)
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode test png: %v", err)
	}
	return buf.Bytes()
}

// imageGenServer is jsonServer's twin, named for readability at this file's
// call sites — image_generation's response never needs real downloadable
// URLs unless a test explicitly serves its own image bytes too (see
// TestImagePluginExecuteHappyPath/PartialDownloadFailure/SourceImageAssetID
// below, which build a multi-route mux instead of using this).
func imageGenServer(t *testing.T, handler http.HandlerFunc) (*Client, func()) {
	return jsonServer(t, handler)
}

// newImageMux starts the httptest.Server first so handler closures can
// close over its own srv.URL when building absolute image_urls in their
// response bodies — registering routes on the mux after Listen/Serve begins
// is safe here since no request is ever made until the test calls
// plugin.Execute, well after every route below is registered.
func newImageMux(t *testing.T) (mux *http.ServeMux, srv *httptest.Server, closeFn func()) {
	t.Helper()
	mux = http.NewServeMux()
	srv = httptest.NewServer(mux)
	return mux, srv, srv.Close
}

func TestImagePluginExecuteHappyPath(t *testing.T) {
	mux, srv, closeFn := newImageMux(t)
	defer closeFn()
	mux.HandleFunc("/img/a.png", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(pngBytes(t, 8, 6))
	})
	mux.HandleFunc("/img/b.png", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(pngBytes(t, 8, 6))
	})
	var gotN int
	mux.HandleFunc("/v1/image_generation", func(w http.ResponseWriter, req *http.Request) {
		var body ImageGenerationRequest
		_ = json.NewDecoder(req.Body).Decode(&body)
		gotN = body.N
		w.Write([]byte(`{"id":"img-1","data":{"image_urls":["` + srv.URL + `/img/a.png","` + srv.URL + `/img/b.png"]},"base_resp":{"status_code":0}}`))
	})
	client := NewClient(srv.URL, "test-api-key")

	sink := &fakeSink{}
	plugin := NewImagePlugin(client, sink, newFakeReader())
	req := execRequest(t, "task-1", map[string]any{"prompt": "a cat", "n": "2", "user-id": "7"})

	out, err := plugin.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gotN != 2 {
		t.Errorf("requested N = %d, want 2", gotN)
	}
	if out.Code != 0 {
		t.Fatalf("Code = %d, Message = %q, want success", out.Code, out.Message)
	}
	if got := outputValue[int](t, out, "success-count"); got != 2 {
		t.Errorf("success-count = %d, want 2", got)
	}
	if got := outputValue[[]string](t, out, "asset-ids"); len(got) != 2 {
		t.Errorf("asset-ids = %v, want 2 entries", got)
	}
	if len(sink.materialized) != 2 {
		t.Fatalf("sink got %d MaterializeBytes calls, want 2", len(sink.materialized))
	}
	if sink.materialized[0].UserID != 7 {
		t.Errorf("UserID = %d, want 7 (parsed from user-id)", sink.materialized[0].UserID)
	}
}

func TestImagePluginExecuteNClamping(t *testing.T) {
	var gotN int
	client, closeFn := imageGenServer(t, func(w http.ResponseWriter, req *http.Request) {
		var body ImageGenerationRequest
		_ = json.NewDecoder(req.Body).Decode(&body)
		gotN = body.N
		w.Write([]byte(`{"id":"img-1","data":{"image_urls":[]},"base_resp":{"status_code":0}}`))
	})
	defer closeFn()

	plugin := NewImagePlugin(client, &fakeSink{}, newFakeReader())
	req := execRequest(t, "task-1", map[string]any{"prompt": "x", "n": "99"})
	if _, err := plugin.Execute(context.Background(), req); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gotN != 9 { // capability.ImageMaxN
		t.Errorf("N sent upstream = %d, want clamped to 9", gotN)
	}
}

func TestImagePluginExecuteNDefaultsToOne(t *testing.T) {
	var gotN int
	client, closeFn := imageGenServer(t, func(w http.ResponseWriter, req *http.Request) {
		var body ImageGenerationRequest
		_ = json.NewDecoder(req.Body).Decode(&body)
		gotN = body.N
		w.Write([]byte(`{"id":"img-1","data":{"image_urls":[]},"base_resp":{"status_code":0}}`))
	})
	defer closeFn()

	plugin := NewImagePlugin(client, &fakeSink{}, newFakeReader())
	req := execRequest(t, "task-1", map[string]any{"prompt": "x"}) // n omitted entirely
	if _, err := plugin.Execute(context.Background(), req); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gotN != 1 {
		t.Errorf("N sent upstream = %d, want 1 (default)", gotN)
	}
}

func TestImagePluginExecutePromptTruncation(t *testing.T) {
	var gotPromptRunes int
	client, closeFn := imageGenServer(t, func(w http.ResponseWriter, req *http.Request) {
		var body ImageGenerationRequest
		_ = json.NewDecoder(req.Body).Decode(&body)
		gotPromptRunes = len([]rune(body.Prompt))
		w.Write([]byte(`{"id":"img-1","data":{"image_urls":[]},"base_resp":{"status_code":0}}`))
	})
	defer closeFn()

	plugin := NewImagePlugin(client, &fakeSink{}, newFakeReader())
	req := execRequest(t, "task-1", map[string]any{"prompt": strings.Repeat("a", 2000)})
	if _, err := plugin.Execute(context.Background(), req); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gotPromptRunes != 1500 { // capability.ImageMaxPromptChars
		t.Errorf("prompt sent upstream = %d runes, want 1500 (truncation cap)", gotPromptRunes)
	}
}

func TestImagePluginExecuteStyleDefaultWeight(t *testing.T) {
	var gotStyle *ImageStyle
	client, closeFn := imageGenServer(t, func(w http.ResponseWriter, req *http.Request) {
		var body ImageGenerationRequest
		_ = json.NewDecoder(req.Body).Decode(&body)
		gotStyle = body.Style
		w.Write([]byte(`{"id":"img-1","data":{"image_urls":[]},"base_resp":{"status_code":0}}`))
	})
	defer closeFn()

	plugin := NewImagePlugin(client, &fakeSink{}, newFakeReader())
	req := execRequest(t, "task-1", map[string]any{
		"prompt": "x", "model": "image-01-live", "style-type": "anime",
	})
	if _, err := plugin.Execute(context.Background(), req); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gotStyle == nil || gotStyle.StyleType != "anime" || gotStyle.StyleWeight != 0.8 {
		t.Errorf("style = %+v, want {anime, 0.8} (default weight)", gotStyle)
	}
}

// TestImagePluginExecuteErrorClassification walks PRD §10.4's full table —
// each MiniMax status_code must map to exactly the documented ExecCode.
func TestImagePluginExecuteErrorClassification(t *testing.T) {
	cases := []struct {
		statusCode int
		wantCode   int
	}{
		{1002, model.ExecCodeError},  // rate_limited
		{1008, model.ExecCodeFailed}, // insufficient_balance
		{1026, model.ExecCodeFailed}, // sensitive_content
		{1004, model.ExecCodeFailed}, // auth_error
		{2049, model.ExecCodeFailed}, // auth_error
		{2013, model.ExecCodeFailed}, // bad_params
		{9999, model.ExecCodeError},  // unclassified
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("status_code=%d", tc.statusCode), func(t *testing.T) {
			client, closeFn := imageGenServer(t, func(w http.ResponseWriter, req *http.Request) {
				w.Write([]byte(fmt.Sprintf(`{"id":"img-1","base_resp":{"status_code":%d,"status_msg":"x"}}`, tc.statusCode)))
			})
			defer closeFn()

			plugin := NewImagePlugin(client, &fakeSink{}, newFakeReader())
			req := execRequest(t, "task-1", map[string]any{"prompt": "x"})
			out, err := plugin.Execute(context.Background(), req)
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if out.Code != tc.wantCode {
				t.Errorf("Code = %d, want %d (message: %q)", out.Code, tc.wantCode, out.Message)
			}
		})
	}
}

func TestImagePluginExecutePartialDownloadFailure(t *testing.T) {
	mux, srv, closeFn := newImageMux(t)
	defer closeFn()
	mux.HandleFunc("/img/ok.png", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(pngBytes(t, 4, 4))
	})
	mux.HandleFunc("/img/missing.png", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/v1/image_generation", func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte(`{"id":"img-1","data":{"image_urls":["` + srv.URL + `/img/ok.png","` + srv.URL + `/img/missing.png"]},"base_resp":{"status_code":0}}`))
	})
	client := NewClient(srv.URL, "test-api-key")

	sink := &fakeSink{}
	plugin := NewImagePlugin(client, sink, newFakeReader())
	req := execRequest(t, "task-1", map[string]any{"prompt": "x", "n": "2"})

	out, err := plugin.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := outputValue[int](t, out, "success-count"); got != 1 {
		t.Errorf("success-count = %d, want 1 (one of two downloads failed)", got)
	}
	if got := outputValue[int](t, out, "failed-count"); got != 1 {
		t.Errorf("failed-count = %d, want 1", got)
	}
	if len(sink.materialized) != 1 {
		t.Errorf("sink got %d MaterializeBytes calls, want 1 (only the successful download)", len(sink.materialized))
	}
}

func TestImagePluginExecuteSourceImageAssetID(t *testing.T) {
	mux, srv, closeFn := newImageMux(t)
	defer closeFn()
	mux.HandleFunc("/source.png", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(pngBytes(t, 2, 2))
	})
	var gotSubjectRef []SubjectReferenceItem
	mux.HandleFunc("/v1/image_generation", func(w http.ResponseWriter, req *http.Request) {
		var body ImageGenerationRequest
		_ = json.NewDecoder(req.Body).Decode(&body)
		gotSubjectRef = body.SubjectReference
		w.Write([]byte(`{"id":"img-1","data":{"image_urls":[]},"base_resp":{"status_code":0}}`))
	})
	client := NewClient(srv.URL, "test-api-key")

	reader := newFakeReader()
	reader.set("src-asset", srv.URL+"/source.png")
	plugin := NewImagePlugin(client, &fakeSink{}, reader)

	req := execRequest(t, "task-1", map[string]any{"prompt": "x", "source-image-asset-id": "src-asset"})
	out, err := plugin.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Code != 0 {
		t.Fatalf("Code = %d, Message = %q, want success", out.Code, out.Message)
	}
	if len(gotSubjectRef) != 1 || gotSubjectRef[0].Type != "character" || !strings.HasPrefix(gotSubjectRef[0].ImageFile, "data:image/png;base64,") {
		t.Errorf("subject_reference = %+v, want one character-type data URI", gotSubjectRef)
	}
}

func TestImagePluginExecuteSourceImageAssetIDNotFound(t *testing.T) {
	client, closeFn := imageGenServer(t, func(w http.ResponseWriter, req *http.Request) {
		t.Fatal("image_generation should never be called when the source asset can't be resolved")
	})
	defer closeFn()

	plugin := NewImagePlugin(client, &fakeSink{}, newFakeReader()) // empty reader — every lookup fails
	req := execRequest(t, "task-1", map[string]any{"prompt": "x", "source-image-asset-id": "does-not-exist"})
	out, err := plugin.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Code != model.ExecCodeError {
		t.Errorf("Code = %d, want ExecCodeError", out.Code)
	}
}
