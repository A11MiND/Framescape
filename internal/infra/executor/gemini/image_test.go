package gemini

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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

// generateContentResponse builds a fake Vertex AI generateContent response
// body containing exactly one inline-data image part — see
// generateContentResponseFromVertex's own pass-through behavior (verified
// against the vendored SDK source) for why this top-level "candidates" shape
// survives the SDK's internal from-Vertex conversion unchanged.
func generateContentResponse(imgData []byte, mime string) string {
	body := map[string]any{
		"candidates": []any{
			map[string]any{
				"content": map[string]any{
					"role": "model",
					"parts": []any{
						map[string]any{"inlineData": map[string]any{
							"data":     base64.StdEncoding.EncodeToString(imgData),
							"mimeType": mime,
						}},
					},
				},
				"finishReason": "STOP",
			},
		},
	}
	raw, _ := json.Marshal(body)
	return string(raw)
}

func TestImagePluginExecuteHappyPath(t *testing.T) {
	var gotBody []byte
	client, closeFn := geminiServer(t, func(w http.ResponseWriter, req *http.Request) {
		gotBody, _ = io.ReadAll(req.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(generateContentResponse(pngBytes(t, 8, 6), "image/png")))
	})
	defer closeFn()

	sink := &fakeSink{}
	plugin := NewImagePlugin(client, sink, newFakeReader(), nil)
	req := execRequest(t, "task-1", map[string]any{"prompt": "a cat astronaut", "n": "1", "user-id": "7"})

	out, err := plugin.Execute(t.Context(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Code != 0 {
		t.Fatalf("Code = %d, Message = %q, want success", out.Code, out.Message)
	}
	if got := outputValue[int](t, out, "success-count"); got != 1 {
		t.Errorf("success-count = %d, want 1", got)
	}
	if got := outputValue[string](t, out, "asset-id"); got == "" {
		t.Errorf("asset-id should be set")
	}
	if len(sink.materialized) != 1 || sink.materialized[0].Mime != "image/png" {
		t.Fatalf("materialized = %+v, want one image/png asset", sink.materialized)
	}
	if !strings.Contains(string(gotBody), "a cat astronaut") {
		t.Errorf("request body should contain the prompt, got %s", gotBody)
	}
}

func TestImagePluginExecuteWithReferenceImage(t *testing.T) {
	refBytes := pngBytes(t, 4, 4)
	refSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(refBytes)
	}))
	defer refSrv.Close()

	var gotBody []byte
	client, closeFn := geminiServer(t, func(w http.ResponseWriter, req *http.Request) {
		gotBody, _ = io.ReadAll(req.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(generateContentResponse(pngBytes(t, 8, 6), "image/png")))
	})
	defer closeFn()

	sink := &fakeSink{}
	reader := newFakeReader()
	reader.set("ref-asset", refSrv.URL)
	plugin := NewImagePlugin(client, sink, reader, nil)
	req := execRequest(t, "task-1", map[string]any{
		"prompt": "same character, different scene", "n": "1", "user-id": "7",
		"source-image-asset-id": "ref-asset",
	})

	out, err := plugin.Execute(t.Context(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Code != 0 {
		t.Fatalf("Code = %d, Message = %q, want success", out.Code, out.Message)
	}
	if !strings.Contains(string(gotBody), base64.StdEncoding.EncodeToString(refBytes)) {
		t.Errorf("request body should embed the reference image bytes")
	}
}

// TestImagePluginExecuteAllAttemptsFail_SurfacesRealError covers a real
// failure found live: a comic4 panel (n=1) whose sole GenerateImage call
// failed came back as an opaque "0 of 1 succeeded" with no error text
// anywhere to debug from. Every attempt failing outright (as opposed to a
// materialize hiccup after a successful generation) must surface the actual
// error message via ExecOutputs, not just a bare failed phaseCondition.
func TestImagePluginExecuteAllAttemptsFail_SurfacesRealError(t *testing.T) {
	client, closeFn := geminiServer(t, func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"promptFeedback":{"blockReason":"SAFETY"}}`))
	})
	defer closeFn()

	sink := &fakeSink{}
	plugin := NewImagePlugin(client, sink, newFakeReader(), nil)
	req := execRequest(t, "task-1", map[string]any{"prompt": "a dog", "n": "1", "user-id": "7"})

	out, err := plugin.Execute(t.Context(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Code == 0 {
		t.Fatalf("Code = 0, want a non-zero error code when every attempt fails")
	}
	if !strings.Contains(out.Message, "SAFETY") {
		t.Errorf("Message = %q, should surface the real block reason", out.Message)
	}
}

// TestImagePluginExecuteMultipleReferenceImages covers image.single-gemini's
// own reason for existing (ImageConfig's own doc): a scene needing more than
// one bound character at once, embedding all of them as separate inline
// image parts in one call — something MiniMax's subject_reference can never
// do (exactly one reference image per call).
func TestImagePluginExecuteMultipleReferenceImages(t *testing.T) {
	ref1 := pngBytes(t, 4, 4)
	ref2 := pngBytes(t, 6, 6)
	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(ref1)
	}))
	defer srv1.Close()
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(ref2)
	}))
	defer srv2.Close()

	var gotBody []byte
	client, closeFn := geminiServer(t, func(w http.ResponseWriter, req *http.Request) {
		gotBody, _ = io.ReadAll(req.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(generateContentResponse(pngBytes(t, 8, 6), "image/png")))
	})
	defer closeFn()

	sink := &fakeSink{}
	reader := newFakeReader()
	reader.set("char-a", srv1.URL)
	reader.set("char-b", srv2.URL)
	plugin := NewImagePlugin(client, sink, reader, nil)
	req := execRequest(t, "task-1", map[string]any{
		"prompt": "the two characters face off", "n": "1", "user-id": "7",
		"reference-image-asset-ids": []string{"char-a", "char-b"},
		"aspect-ratio":              "16:9",
	})

	out, err := plugin.Execute(t.Context(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Code != 0 {
		t.Fatalf("Code = %d, Message = %q, want success", out.Code, out.Message)
	}
	body := string(gotBody)
	if !strings.Contains(body, base64.StdEncoding.EncodeToString(ref1)) || !strings.Contains(body, base64.StdEncoding.EncodeToString(ref2)) {
		t.Errorf("request body should embed both reference images")
	}
	if !strings.Contains(body, "16:9") {
		t.Errorf("request body should carry the requested aspect ratio, got %s", body)
	}
}

func TestImagePluginExecuteMultipleImages_PartialFailureCountsAgainstSuccess(t *testing.T) {
	calls := 0
	client, closeFn := geminiServer(t, func(w http.ResponseWriter, req *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 2 {
			// A response with no candidates at all (e.g. safety block) —
			// GenerateImage should surface this as an error, and Execute
			// should count it against success rather than fail the whole call.
			_, _ = w.Write([]byte(`{"promptFeedback":{"blockReason":"SAFETY"}}`))
			return
		}
		_, _ = w.Write([]byte(generateContentResponse(pngBytes(t, 8, 6), "image/png")))
	})
	defer closeFn()

	sink := &fakeSink{}
	plugin := NewImagePlugin(client, sink, newFakeReader(), nil)
	req := execRequest(t, "task-1", map[string]any{"prompt": "a dog", "n": "3", "user-id": "7"})

	out, err := plugin.Execute(t.Context(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := outputValue[int](t, out, "success-count"); got != 2 {
		t.Errorf("success-count = %d, want 2", got)
	}
	if got := outputValue[int](t, out, "failed-count"); got != 1 {
		t.Errorf("failed-count = %d, want 1", got)
	}
	if got := outputValue[int](t, out, "requested-n"); got != 3 {
		t.Errorf("requested-n = %d, want 3", got)
	}
}
