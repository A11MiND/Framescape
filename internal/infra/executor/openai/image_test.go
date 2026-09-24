package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aigc-platform/internal/infra/executor/assetstore"
	"aigc-platform/internal/infra/executor/spi/executor"
	"aigc-platform/internal/infra/executor/spi/model"
)

const testModel = "gpt-image-2.5-flare"

func pngBytes(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 16, 16))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

type testStore struct {
	fail    bool
	refBase string
	asset   assetstore.NewAssetBytes
}

func (s *testStore) Materialize(context.Context, assetstore.NewAsset) (string, error) {
	panic("unused")
}
func (s *testStore) MaterializeBytes(_ context.Context, a assetstore.NewAssetBytes) (string, error) {
	s.asset = a
	_, _ = io.ReadAll(a.Body)
	if s.fail {
		return "", fmt.Errorf("storage down")
	}
	return "saved-image", nil
}
func (s *testStore) PublicURL(_ context.Context, id string) (string, error) {
	return s.refBase + "/" + id + ".png", nil
}

// refServer stands in for object storage serving reference images.
func refServer(t *testing.T) *httptest.Server {
	data := pngBytes(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write(data) }))
	t.Cleanup(srv.Close)
	return srv
}

func request(prompt string, refs ...string) *executor.ExecuteRequest {
	if refs == nil {
		refs = []string{}
	}
	p := []model.Parameter{}
	for k, v := range map[string]any{"prompt": prompt, "user-id": "7", "reference-image-asset-ids": refs} {
		b, _ := json.Marshal(v)
		p = append(p, model.Parameter{Name: k, Value: b})
	}
	return &executor.ExecuteRequest{TaskRunID: "test-task", Inputs: &model.Inputs{Parameters: p}}
}

func value(t *testing.T, o *model.ExecOutputs, k string) any {
	t.Helper()
	for _, p := range o.Parameters {
		if p.Name == k {
			var v any
			json.Unmarshal(p.Value, &v)
			return v
		}
	}
	t.Fatalf("missing %s", k)
	return nil
}

func imageResponse(t *testing.T, usage map[string]any) string {
	body := map[string]any{"data": []any{map[string]any{"b64_json": base64.StdEncoding.EncodeToString(pngBytes(t))}}}
	if usage != nil {
		body["usage"] = usage
	}
	out, _ := json.Marshal(body)
	return string(out)
}

// Matches a real 2.5-flare call: 748 text-in + 1372 image-out = $0.0449.
var realUsage = map[string]any{"input_tokens": 748, "output_tokens": 1372, "total_tokens": 2120, "input_tokens_details": map[string]any{"text_tokens": 748, "image_tokens": 0}}

func newPlugin(sink *testStore, base string) *ImagePlugin {
	return NewImagePlugin(Config{APIKey: "secret", Model: testModel, BaseURL: base, USDToCNY: 7, ReserveUSD: 0.5}, sink, sink)
}

func near(a any, want float64) bool { f, ok := a.(float64); return ok && math.Abs(f-want) < 1e-9 }

func TestGenerationWithoutReferences(t *testing.T) {
	calls := 0
	prompt := strings.Repeat("中文原始要求", 500)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/images/generations" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("wrong endpoint/auth: %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["prompt"] != prompt || body["model"] != testModel || body["n"] != float64(1) || body["size"] != "1536x1024" || body["quality"] != "high" || body["background"] != "opaque" || body["output_format"] != "png" {
			t.Errorf("wrong request shape: %v", body["size"])
		}
		for _, k := range []string{"aspect_ratio", "provider", "input_references"} {
			if _, ok := body[k]; ok {
				t.Errorf("OpenRouter-only field %s sent", k)
			}
		}
		io.WriteString(w, imageResponse(t, realUsage))
	}))
	defer srv.Close()
	sink := &testStore{}
	out, err := newPlugin(sink, srv.URL).Execute(t.Context(), request(prompt))
	if err != nil || out.Code != 0 || calls != 1 {
		t.Fatalf("execute: %v %+v calls=%d", err, out, calls)
	}
	if value(t, out, "asset-id") != "saved-image" || value(t, out, "usage-known") != true {
		t.Fatal("wrong outputs")
	}
	if !near(value(t, out, "cost-usd"), 0.0449) || !near(value(t, out, "cost-yuan"), 0.0449*7) {
		t.Fatalf("cost = %v", value(t, out, "cost-usd"))
	}
	if sink.asset.Width != 16 || sink.asset.Meta["prompt"] != prompt || sink.asset.UserID != 7 {
		t.Fatal("asset lost metadata")
	}
}

func TestEditUploadsReferenceBytes(t *testing.T) {
	refs := refServer(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/images/edits" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			t.Fatal(err)
		}
		if r.FormValue("size") != "1536x1024" || r.FormValue("model") != testModel || r.FormValue("prompt") != "comic" {
			t.Error("wrong form fields")
		}
		files := r.MultipartForm.File["image[]"]
		if len(files) != 3 {
			t.Fatalf("images = %d", len(files))
		}
		for _, f := range files {
			if f.Header.Get("Content-Type") != "image/png" {
				t.Errorf("part content type = %q", f.Header.Get("Content-Type"))
			}
		}
		io.WriteString(w, imageResponse(t, map[string]any{"input_tokens": 1000, "output_tokens": 1000, "input_tokens_details": map[string]any{"text_tokens": 200, "image_tokens": 800}}))
	}))
	defer srv.Close()
	sink := &testStore{refBase: refs.URL}
	out, err := newPlugin(sink, srv.URL).Execute(t.Context(), request("comic", "a", "b", "page"))
	if err != nil || out.Code != 0 {
		t.Fatalf("execute: %v %+v", err, out)
	}
	if want := (200*5 + 800*8 + 1000*30) / 1e6; !near(value(t, out, "cost-usd"), want) {
		t.Fatalf("cost = %v want %v", value(t, out, "cost-usd"), want)
	}
}

func TestSixteenReferencesIncludingWebP(t *testing.T) {
	webp := append([]byte("RIFF\x00\x00\x00\x00WEBPVP8 "), make([]byte, 32)...)
	refs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "webp") {
			w.Write(webp)
			return
		}
		w.Write(pngBytes(t))
	}))
	defer refs.Close()
	var types []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			t.Fatal(err)
		}
		for _, f := range r.MultipartForm.File["image[]"] {
			types = append(types, f.Header.Get("Content-Type"))
		}
		io.WriteString(w, imageResponse(t, nil))
	}))
	defer srv.Close()
	ids := []string{"style.webp"}
	for len(ids) < 16 {
		ids = append(ids, fmt.Sprintf("ref-%d", len(ids)))
	}
	sink := &testStore{refBase: refs.URL}
	p := NewImagePlugin(Config{APIKey: "secret", Model: testModel, BaseURL: srv.URL, USDToCNY: 7, ReserveUSD: 0.5, PerRefUSD: 0.1}, sink, sink)
	out, err := p.Execute(t.Context(), request("comic", ids...))
	if err != nil || out.Code != 0 {
		t.Fatalf("execute: %v %+v", err, out)
	}
	if len(types) != 16 || types[0] != "image/webp" || types[1] != "image/png" {
		t.Fatalf("uploaded parts = %v", types)
	}
	// No usage: the reservation for 16 references is billed, same as jobsvc's hold.
	if !near(value(t, out, "cost-usd"), 0.5+16*0.1) {
		t.Fatalf("cost = %v", value(t, out, "cost-usd"))
	}
	if out, _ := p.Execute(t.Context(), request("comic", append(ids, "seventeenth")...)); out.Code == 0 {
		t.Fatal("accepted more images than OpenAI allows")
	}
}

func TestMissingUsageBillsReserve(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, imageResponse(t, nil))
	}))
	defer srv.Close()
	sink := &testStore{}
	out, err := newPlugin(sink, srv.URL).Execute(t.Context(), request("comic"))
	if err != nil || out.Code != 0 {
		t.Fatal(out, err)
	}
	if value(t, out, "usage-known") != false || !near(value(t, out, "cost-yuan"), 3.5) {
		t.Fatal("a paid image without usage must bill the reservation, not zero")
	}
}

func TestErrorsDoNotRetryOrLeak(t *testing.T) {
	for _, tc := range []struct {
		name        string
		status      int
		body        string
		storageFail bool
		billed      bool
		message     string
	}{
		{"rate limit", 429, `{"error":{"message":"secret prompt","code":"rate_limit_exceeded"}}`, false, false, "rate_limit_exceeded"},
		{"quota", 429, `{"error":{"message":"secret","type":"insufficient_quota","code":"insufficient_quota"}}`, false, false, "insufficient_quota"},
		{"unauthorized", 401, "secret", false, false, "HTTP 401"},
		{"moderation", 400, `{"error":{"message":"secret prompt","code":"moderation_blocked"}}`, false, false, "sensitive_content:"},
		{"invalid json", 200, "secret", false, false, "invalid JSON"},
		{"empty images", 200, `{"data":[],"usage":{"input_tokens":100,"output_tokens":100}}`, false, true, "exactly one"},
		{"bad base64", 200, `{"data":[{"b64_json":"!"}],"usage":{"input_tokens":100,"output_tokens":100}}`, false, true, "invalid image"},
		{"storage failed", 200, imageResponse(t, realUsage), true, true, "storage failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			sink := &testStore{fail: tc.storageFail}
			out, err := newPlugin(sink, srv.URL).Execute(t.Context(), request("comic"))
			if err != nil || out.Code == 0 || calls != 1 {
				t.Fatalf("wrong error: %v %+v %d", err, out, calls)
			}
			if strings.Contains(out.Message, "secret") || !strings.Contains(out.Message, tc.message) {
				t.Fatalf("message = %q", out.Message)
			}
			if tc.billed {
				if f, _ := value(t, out, "cost-yuan").(float64); f <= 0 {
					t.Fatal("lost billable usage on failure")
				}
			} else {
				for _, p := range out.Parameters {
					if p.Name == "cost-yuan" {
						t.Fatal("billed a call OpenAI rejected")
					}
				}
			}
		})
	}
}

func TestLocalValidationNoRequest(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer srv.Close()
	sink := &testStore{refBase: "http://127.0.0.1:1"}
	p := newPlugin(sink, srv.URL)
	for _, prompt := range []string{"", strings.Repeat("字", 32001)} {
		if out, err := p.Execute(t.Context(), request(prompt)); err != nil || !strings.Contains(out.Message, "invalid comic") {
			t.Fatal(out, err)
		}
	}
	if out, _ := p.Execute(t.Context(), request("comic", "unreachable")); !strings.Contains(out.Message, "reference image unavailable") {
		t.Fatal(out.Message)
	}
	unpriced := NewImagePlugin(Config{APIKey: "secret", Model: "gpt-image-9", BaseURL: srv.URL, USDToCNY: 7, ReserveUSD: .5}, sink, sink)
	if out, _ := unpriced.Execute(t.Context(), request("comic")); !strings.Contains(out.Message, "no configured price") {
		t.Fatal(out.Message)
	}
	if calls != 0 {
		t.Fatalf("made %d paid calls on invalid input", calls)
	}
}

func TestPriceKnown(t *testing.T) {
	for model, want := range map[string]bool{"gpt-image-2.5-flare": true, "gpt-image-2.5-flare-2026-09-08": true, "gpt-image-2.5-sunburst": true, "gpt-image-2.5-flarex": false, "gpt-image-1": false, "openai/gpt-image-2.5-flare": false} {
		if PriceKnown(model) != want {
			t.Errorf("PriceKnown(%q) != %v", model, want)
		}
	}
}
