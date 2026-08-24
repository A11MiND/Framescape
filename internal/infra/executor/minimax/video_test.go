package minimax

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BabySid/aether/model"
)

// --- pure logic ---

func TestNormalizeDuration(t *testing.T) {
	cases := map[string]int{
		"":     5, // parse failure -> default
		"0":    5, // <=0 -> default
		"-3":   5,
		"7":    7,
		"2":    4,  // below VideoDurationMin -> clamped up
		"9999": 15, // above VideoDurationMax -> clamped down
	}
	for in, want := range cases {
		if got := normalizeDuration(in); got != want {
			t.Errorf("normalizeDuration(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestNormalizeResolution(t *testing.T) {
	cases := map[string]string{
		"2K":      "2K",
		"768P":    "768P",
		"":        "768P",
		"garbage": "768P", // defends the costPerSecondYuan lookup — see normalizeResolution's own doc
	}
	for in, want := range cases {
		if got := normalizeResolution(in); got != want {
			t.Errorf("normalizeResolution(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsTerminalVideoStatus(t *testing.T) {
	terminal := []string{"succeeded", "failed", "cancelled"}
	nonTerminal := []string{"queued", "running", "", "unknown"}
	for _, s := range terminal {
		if !isTerminalVideoStatus(s) {
			t.Errorf("isTerminalVideoStatus(%q) = false, want true", s)
		}
	}
	for _, s := range nonTerminal {
		if isTerminalVideoStatus(s) {
			t.Errorf("isTerminalVideoStatus(%q) = true, want false", s)
		}
	}
}

// TestClassifyVideoError walks §10.4's video column — every documented HTTP
// status maps to its own ExecCode, and a plain transport failure (no HTTP
// response at all) is always retryable.
func TestClassifyVideoError(t *testing.T) {
	cases := []struct {
		status   int
		wantCode int
	}{
		{429, model.ExecCodeError},
		{401, model.ExecCodeFailed},
		{402, model.ExecCodeFailed},
		{422, model.ExecCodeFailed},
		{400, model.ExecCodeFailed},
		{500, model.ExecCodeError},
		{529, model.ExecCodeError},
		{599, model.ExecCodeError}, // unclassified
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("status=%d", tc.status), func(t *testing.T) {
			out := classifyVideoError(&HTTPStatusError{StatusCode: tc.status, Body: "x"})
			if out.Code != tc.wantCode {
				t.Errorf("Code = %d, want %d", out.Code, tc.wantCode)
			}
		})
	}
}

func TestClassifyVideoErrorTransportFailure(t *testing.T) {
	out := classifyVideoError(errors.New("dial tcp: connection refused"))
	if out.Code != model.ExecCodeError {
		t.Errorf("Code = %d, want ExecCodeError for a plain transport error", out.Code)
	}
	if !strings.HasPrefix(out.Message, "transport:") {
		t.Errorf("Message = %q, want a transport: prefix", out.Message)
	}
}

// --- buildContent: §3.2's mode/ratio rules ---

func TestBuildContentModeMutualExclusion(t *testing.T) {
	b := &videoBase{}
	_, _, _, errOut := b.buildContent(context.Background(), videoRefs{
		Prompt:                 "x",
		FirstFrameAssetID:      "a",
		ReferenceImageAssetIDs: []string{"b"},
	})
	if errOut == nil || errOut.Code != model.ExecCodeFailed || !strings.Contains(errOut.Message, "mutual_exclusion") {
		t.Fatalf("errOut = %+v, want a mutual_exclusion Failed error", errOut)
	}
}

func TestBuildContentT2VARequiresNonAdaptiveRatio(t *testing.T) {
	b := &videoBase{}
	for _, ratio := range []string{"", "adaptive"} {
		_, _, _, errOut := b.buildContent(context.Background(), videoRefs{Prompt: "x", Ratio: ratio})
		if errOut == nil || !strings.Contains(errOut.Message, "bad_params") {
			t.Errorf("ratio=%q: errOut = %+v, want a bad_params error (t2va requires a real ratio)", ratio, errOut)
		}
	}

	content, mode, ratio, errOut := b.buildContent(context.Background(), videoRefs{Prompt: "a cat", Ratio: "16:9"})
	if errOut != nil {
		t.Fatalf("errOut = %+v, want nil", errOut)
	}
	if mode != "t2va" || ratio != "16:9" {
		t.Errorf("mode/ratio = %q/%q, want t2va/16:9", mode, ratio)
	}
	if len(content) != 1 || content[0].Type != "text" || content[0].Text != "a cat" {
		t.Errorf("content = %+v, want exactly one text item", content)
	}
}

// TestBuildContentI2VAForcesAdaptiveRatio covers §3.2's "传别的会被忽略" —
// i2va must normalize any caller-supplied ratio to "adaptive" rather than
// silently sending a value MiniMax ignores. FirstFrameAssetID must resolve
// through refItem for real (same fake upload/cache/reader wiring as the
// r2va test below), so this exercises the actual i2va code path, not just
// a hand-picked mode value.
func TestBuildContentI2VAForcesAdaptiveRatio(t *testing.T) {
	b := &videoBase{}
	uploadClient, uploadClose := jsonServer(t, func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte(`{"file":{"file_id":9},"base_resp":{"status_code":0}}`))
	})
	defer uploadClose()
	assetURL, assetClose := servePNG(t)
	defer assetClose()
	reader := newFakeReader()
	reader.set("first-frame", assetURL)
	b.client = uploadClient
	b.reader = reader
	b.cache = newFakeFileCache()

	content, mode, ratio, errOut := b.buildContent(context.Background(), videoRefs{
		Prompt: "x", Ratio: "16:9", FirstFrameAssetID: "first-frame",
	})
	if errOut != nil {
		t.Fatalf("errOut = %+v, want nil", errOut)
	}
	if mode != "i2va" {
		t.Errorf("mode = %q, want i2va", mode)
	}
	if ratio != "adaptive" {
		t.Errorf("ratio = %q, want forced to adaptive even though 16:9 was supplied", ratio)
	}
	if len(content) != 2 || content[1].Role != "first_frame" {
		t.Errorf("content = %+v, want a text item plus one first_frame item", content)
	}
}

// TestBuildContentPureFirstLastFrame is F6.3's own verification: "仅首尾帧"
// (first_frame AND last_frame together, no other reference) has always run
// live inside video.sequence's shot chain, but had never been independently
// checked in isolation — this pins i2va mode, both items landing with the
// right roles in the right order, and the ratio forced to adaptive exactly
// as the single-first-frame case above.
func TestBuildContentPureFirstLastFrame(t *testing.T) {
	b := &videoBase{}
	uploadClient, uploadClose := jsonServer(t, func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte(`{"file":{"file_id":11},"base_resp":{"status_code":0}}`))
	})
	defer uploadClose()
	firstURL, firstClose := servePNG(t)
	defer firstClose()
	lastURL, lastClose := servePNG(t)
	defer lastClose()
	reader := newFakeReader()
	reader.set("first", firstURL)
	reader.set("last", lastURL)
	b.client = uploadClient
	b.reader = reader
	b.cache = newFakeFileCache()

	content, mode, ratio, errOut := b.buildContent(context.Background(), videoRefs{
		Prompt: "a sunrise", Ratio: "16:9", FirstFrameAssetID: "first", LastFrameAssetID: "last",
	})
	if errOut != nil {
		t.Fatalf("errOut = %+v, want nil", errOut)
	}
	if mode != "i2va" {
		t.Errorf("mode = %q, want i2va", mode)
	}
	if ratio != "adaptive" {
		t.Errorf("ratio = %q, want forced to adaptive", ratio)
	}
	if len(content) != 3 {
		t.Fatalf("content = %+v, want exactly 3 items (text, first_frame, last_frame)", content)
	}
	if content[1].Role != "first_frame" || content[2].Role != "last_frame" {
		t.Errorf("roles = [%q, %q], want [first_frame, last_frame] in that order", content[1].Role, content[2].Role)
	}
	if content[1].ImageURL == nil || content[1].ImageURL.URL != "mm_file://11" {
		t.Errorf("first_frame url = %+v, want mm_file://11", content[1].ImageURL)
	}
	if content[2].ImageURL == nil || content[2].ImageURL.URL != "mm_file://11" {
		t.Errorf("last_frame url = %+v, want mm_file://11 (same fake upload response for both)", content[2].ImageURL)
	}
}

func TestBuildContentR2VADefaultsRatioToAdaptive(t *testing.T) {
	b := &videoBase{}
	// Reference-video mode needs refItem, which needs a real client/cache —
	// use asset IDs that resolve through a fake server so this stays a real
	// end-to-end check of the r2va branch, not just the mode switch.
	client, closeFn := jsonServer(t, func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte(`{"file":{"file_id":7},"base_resp":{"status_code":0}}`))
	})
	defer closeFn()
	reader := newFakeReader()
	assetURL, assetClose := servePNG(t)
	defer assetClose()
	reader.set("ref-video", assetURL)
	b.client = client
	b.reader = reader
	b.cache = newFakeFileCache()

	content, mode, ratio, errOut := b.buildContent(context.Background(), videoRefs{
		Prompt: "x", ReferenceVideoAssetIDs: []string{"ref-video"},
	})
	if errOut != nil {
		t.Fatalf("errOut = %+v, want nil", errOut)
	}
	if mode != "r2va" || ratio != "adaptive" {
		t.Errorf("mode/ratio = %q/%q, want r2va/adaptive", mode, ratio)
	}
	if len(content) != 2 || content[1].Type != "video_url" || content[1].Role != "reference_video" {
		t.Errorf("content = %+v, want a text item plus one reference_video item", content)
	}
	if content[1].VideoURL == nil || content[1].VideoURL.URL != "mm_file://7" {
		t.Errorf("video url = %+v, want mm_file://7 (resolved via upload)", content[1].VideoURL)
	}
}

// --- VideoPlugin.Execute end to end ---

// videoTaskServer starts the fake server and wires /v2/video_generation
// (always returning taskID) first — the caller registers
// /v2/query/video_generation/{taskID} (and, if needed, a download route)
// afterward, once srv.URL is known, the same registration-after-Listen
// pattern newImageMux already establishes. Every test here has its query
// handler return a terminal status on the very first call, so
// VideoPlugin.wait returns before ever reaching its 10s poll ticker (wait's
// own doc: the query happens before the select) — keeps these tests fast
// without needing to fake time.
func videoTaskServer(t *testing.T, taskID string) (client *Client, mux *http.ServeMux, srv *httptest.Server) {
	t.Helper()
	mux = http.NewServeMux()
	srv = httptest.NewServer(mux)
	mux.HandleFunc("/v2/video_generation", func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte(`{"task_id":"` + taskID + `","base_resp":{"status_code":0}}`))
	})
	return NewClient(srv.URL, "test-api-key"), mux, srv
}

func TestVideoPluginExecuteHappyPath(t *testing.T) {
	client, mux, srv := videoTaskServer(t, "vt-1")
	defer srv.Close()
	mux.HandleFunc("/v2/query/video_generation/vt-1", func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte(`{"task":{"id":"vt-1","status":"succeeded","content":{"url":"` + srv.URL + `/v.mp4"},"resolution":"768P","duration":5,"usage":{"output_seconds":5}}}`))
	})
	mux.HandleFunc("/v.mp4", func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte("fake-mp4-bytes"))
	})

	sink := &fakeSink{}
	plugin := NewVideoPlugin(client, sink, newFakeReader(), newFakeFileCache(), nil, "", nil, nil)
	req := execRequest(t, "task-1", map[string]any{
		"prompt": "a cat", "duration": "5", "resolution": "768P", "ratio": "16:9", "user-id": "3",
	})

	out, err := plugin.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Code != 0 {
		t.Fatalf("Code = %d, Message = %q, want success", out.Code, out.Message)
	}
	if got := outputValue[string](t, out, "asset-id"); got == "" {
		t.Error("asset-id is empty")
	}
	if got := outputValue[float64](t, out, "cost-yuan"); got != 5*0.50 {
		t.Errorf("cost-yuan = %v, want %v (5s * 768P rate)", got, 5*0.50)
	}
	if len(sink.materialized) != 1 {
		t.Fatalf("sink got %d MaterializeBytes calls, want 1", len(sink.materialized))
	}
	if sink.materialized[0].DurationMs != 5000 {
		t.Errorf("DurationMs = %d, want 5000", sink.materialized[0].DurationMs)
	}
	if sink.materialized[0].UserID != 3 {
		t.Errorf("UserID = %d, want 3", sink.materialized[0].UserID)
	}
}

// TestVideoPluginExecute_RecordsOrphanThenResolves covers the normal (first
// attempt) path with a real OrphanTaskStore wired in: Put must be called
// with the real MiniMax task_id as soon as it's created (not just once
// everything else also succeeds — the whole point is surviving a timeout
// that happens after this point), and Resolve once a terminal MiniMax
// status is actually reached.
func TestVideoPluginExecute_RecordsOrphanThenResolves(t *testing.T) {
	client, mux, srv := videoTaskServer(t, "vt-3")
	defer srv.Close()
	mux.HandleFunc("/v2/query/video_generation/vt-3", func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte(`{"task":{"id":"vt-3","status":"succeeded","content":{"url":"` + srv.URL + `/v.mp4"},"resolution":"768P","duration":5,"usage":{"output_seconds":5}}}`))
	})
	mux.HandleFunc("/v.mp4", func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte("fake-mp4-bytes"))
	})

	orphans := newFakeOrphanStore()
	plugin := NewVideoPlugin(client, &fakeSink{}, newFakeReader(), newFakeFileCache(), nil, "", nil, orphans)
	req := execRequest(t, "task-orphan-1", map[string]any{
		"prompt": "a cat", "duration": "5", "resolution": "768P", "ratio": "16:9",
	})

	out, err := plugin.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Code != 0 {
		t.Fatalf("Code = %d, Message = %q, want success", out.Code, out.Message)
	}
	if len(orphans.puts) != 1 || orphans.puts[0] != "vt-3" {
		t.Errorf("Put calls = %v, want exactly one with vt-3", orphans.puts)
	}
	if len(orphans.resolved) != 1 || orphans.resolved[0] != "task-orphan-1" {
		t.Errorf("Resolve calls = %v, want exactly one with task-orphan-1", orphans.resolved)
	}
}

// TestVideoPluginExecute_RecoversOrphanedTaskOnRetry is the actual bug fix:
// a retry (RetryCount > 0) of a node that left an unresolved MiniMax task_id
// behind must check on that task instead of creating a brand new one — the
// /v2/video_generation handler here fails the test outright if it's ever
// called, since a correct recovery never reaches it.
func TestVideoPluginExecute_RecoversOrphanedTaskOnRetry(t *testing.T) {
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/v2/video_generation", func(w http.ResponseWriter, req *http.Request) {
		t.Fatal("CreateVideoTask must not be called when a recoverable orphan task exists")
	})
	mux.HandleFunc("/v2/query/video_generation/vt-recovered", func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte(`{"task":{"id":"vt-recovered","status":"succeeded","content":{"url":"` + srv.URL + `/v.mp4"},"resolution":"768P","duration":5,"usage":{"output_seconds":5}}}`))
	})
	mux.HandleFunc("/v.mp4", func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte("fake-mp4-bytes"))
	})
	client := NewClient(srv.URL, "test-api-key")

	orphans := newFakeOrphanStore()
	if err := orphans.Put(context.Background(), "task-orphan-2", "vt-recovered"); err != nil {
		t.Fatalf("seed orphan: %v", err)
	}
	putsBeforeExecute := len(orphans.puts)

	plugin := NewVideoPlugin(client, &fakeSink{}, newFakeReader(), newFakeFileCache(), nil, "", nil, orphans)
	req := execRequest(t, "task-orphan-2", map[string]any{
		"prompt": "a cat", "duration": "5", "resolution": "768P", "ratio": "16:9",
	})
	req.RetryCount = 1

	out, err := plugin.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Code != 0 {
		t.Fatalf("Code = %d, Message = %q, want success (recovered)", out.Code, out.Message)
	}
	if len(orphans.puts) != putsBeforeExecute {
		t.Errorf("Put calls after Execute = %v, want no new ones — the orphan should have been recovered, not replaced", orphans.puts)
	}
	if len(orphans.resolved) != 1 || orphans.resolved[0] != "task-orphan-2" {
		t.Errorf("Resolve calls = %v, want exactly one with task-orphan-2", orphans.resolved)
	}
}

func TestVideoPluginExecuteFailedStatus(t *testing.T) {
	client, mux, srv := videoTaskServer(t, "vt-2")
	defer srv.Close()
	mux.HandleFunc("/v2/query/video_generation/vt-2", func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte(`{"task":{"id":"vt-2","status":"failed","error":{"code":"content_moderation","message":"rejected"}}}`))
	})

	plugin := NewVideoPlugin(client, &fakeSink{}, newFakeReader(), newFakeFileCache(), nil, "", nil, nil)
	req := execRequest(t, "task-1", map[string]any{"prompt": "x", "duration": "5", "resolution": "768P", "ratio": "16:9"})
	out, err := plugin.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Code != model.ExecCodeFailed {
		t.Errorf("Code = %d, want ExecCodeFailed", out.Code)
	}
	if !strings.Contains(out.Message, "content_moderation") {
		t.Errorf("Message = %q, want it to carry the task's error code", out.Message)
	}
}

func TestVideoPluginExecuteCancelledStatus(t *testing.T) {
	client, mux, srv := videoTaskServer(t, "vt-3")
	defer srv.Close()
	mux.HandleFunc("/v2/query/video_generation/vt-3", func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte(`{"task":{"id":"vt-3","status":"cancelled"}}`))
	})

	plugin := NewVideoPlugin(client, &fakeSink{}, newFakeReader(), newFakeFileCache(), nil, "", nil, nil)
	req := execRequest(t, "task-1", map[string]any{"prompt": "x", "duration": "5", "resolution": "768P", "ratio": "16:9"})
	out, err := plugin.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Code != model.ExecCodeFailed || out.Message != "cancelled_upstream" {
		t.Errorf("out = %+v, want Failed/cancelled_upstream", out)
	}
}

func TestVideoPluginExecuteCreateTaskHTTPError(t *testing.T) {
	client, closeFn := jsonServer(t, func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("bad key"))
	})
	defer closeFn()

	plugin := NewVideoPlugin(client, &fakeSink{}, newFakeReader(), newFakeFileCache(), nil, "", nil, nil)
	req := execRequest(t, "task-1", map[string]any{"prompt": "x", "duration": "5", "resolution": "768P", "ratio": "16:9"})
	out, err := plugin.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Code != model.ExecCodeFailed { // classifyVideoError's 401 -> auth_error -> Failed
		t.Errorf("Code = %d, want ExecCodeFailed (auth_error)", out.Code)
	}
}

// TestVideoPluginExecuteNoCapacity covers §11.2's "拿不到令牌返回
// ExecCodeError" — a pre-exhausted limiter must stop Execute before it ever
// calls CreateVideoTask.
func TestVideoPluginExecuteNoCapacity(t *testing.T) {
	rc := testRedisClient(t)
	accountID := "httptest-novideo-" + t.Name()
	t.Cleanup(func() { rc.Del(context.Background(), "sem:minimax:video:"+accountID) })
	limiter := NewVideoLimiter(rc, accountID, 1)
	release, ok, err := limiter.Acquire(context.Background(), "someone-elses-task")
	if err != nil || !ok {
		t.Fatalf("pre-fill acquire: ok=%v err=%v", ok, err)
	}
	defer release()

	client, closeFn := jsonServer(t, func(w http.ResponseWriter, req *http.Request) {
		t.Fatal("CreateVideoTask must never be called once the limiter has no capacity")
	})
	defer closeFn()

	plugin := NewVideoPlugin(client, &fakeSink{}, newFakeReader(), newFakeFileCache(), nil, "", limiter, nil)
	req := execRequest(t, "my-task", map[string]any{"prompt": "x", "duration": "5", "resolution": "768P", "ratio": "16:9"})
	out, err := plugin.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Code != model.ExecCodeError || !strings.Contains(out.Message, "no_capacity") {
		t.Errorf("out = %+v, want ExecCodeError/no_capacity", out)
	}
}

// TestVideoRegenPluginForces2K needs to inspect the actual request body sent
// to /v2/video_generation, unlike videoTaskServer's other callers, so it
// wires its own mux directly rather than sharing that fixed-response helper.
func TestVideoRegenPluginForces2K(t *testing.T) {
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	client := NewClient(srv.URL, "test-api-key")

	var gotResolution string
	mux.HandleFunc("/v2/video_generation", func(w http.ResponseWriter, req *http.Request) {
		var body VideoGenerationRequest
		_ = json.NewDecoder(req.Body).Decode(&body)
		gotResolution = body.Resolution
		w.Write([]byte(`{"task_id":"vt-regen","base_resp":{"status_code":0}}`))
	})
	mux.HandleFunc("/v2/query/video_generation/vt-regen", func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte(`{"task":{"id":"vt-regen","status":"succeeded","content":{"url":"` + srv.URL + `/v.mp4"},"resolution":"2K","duration":5,"usage":{"output_seconds":5}}}`))
	})
	mux.HandleFunc("/v.mp4", func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte("fake-mp4-bytes"))
	})

	sink := &fakeSink{}
	plugin := NewVideoRegenPlugin(client, sink, newFakeReader(), newFakeFileCache(), nil, "", nil, nil)
	req := execRequest(t, "task-1", map[string]any{
		"prompt": "x", "duration": "5", "resolution": "768P", "ratio": "16:9", "base-video-asset-id": "orig-asset",
	})
	out, err := plugin.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Code != 0 {
		t.Fatalf("Code = %d, Message = %q, want success", out.Code, out.Message)
	}
	if gotResolution != "2K" {
		t.Errorf("resolution sent upstream = %q, want 2K regardless of the requested 768P input", gotResolution)
	}
	if got := outputValue[float64](t, out, "cost-yuan"); got != 5*0.80 {
		t.Errorf("cost-yuan = %v, want %v (5s * 2K rate, no discounted regen tier)", got, 5*0.80)
	}
	if len(sink.materialized) != 1 || sink.materialized[0].Meta["regen_of_asset_id"] != "orig-asset" {
		t.Errorf("materialized meta = %+v, want regen_of_asset_id=orig-asset", sink.materialized[0].Meta)
	}
}

// TestVideoPluginExecutePureFirstLastFrame is F6.3's full-pipeline
// counterpart to TestBuildContentPureFirstLastFrame — the same "仅首尾帧"
// input run all the way through VideoPlugin.Execute (submit -> wait ->
// materialize), not just the content-building step, confirming the whole
// path a real video.single i2va job takes actually completes successfully.
func TestVideoPluginExecutePureFirstLastFrame(t *testing.T) {
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/v1/files/upload", func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte(`{"file":{"file_id":22},"base_resp":{"status_code":0}}`))
	})
	mux.HandleFunc("/frame.png", func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte("fake-bytes"))
	})
	var gotContent []VideoContentItem
	var gotRatio string
	mux.HandleFunc("/v2/video_generation", func(w http.ResponseWriter, req *http.Request) {
		var body VideoGenerationRequest
		_ = json.NewDecoder(req.Body).Decode(&body)
		gotContent = body.Content
		gotRatio = body.Ratio
		w.Write([]byte(`{"task_id":"vt-f6.3","base_resp":{"status_code":0}}`))
	})
	mux.HandleFunc("/v2/query/video_generation/vt-f6.3", func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte(`{"task":{"id":"vt-f6.3","status":"succeeded","content":{"url":"` + srv.URL + `/v.mp4"},"resolution":"768P","duration":5,"usage":{"output_seconds":5}}}`))
	})
	mux.HandleFunc("/v.mp4", func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte("fake-mp4-bytes"))
	})
	client := NewClient(srv.URL, "test-api-key")

	reader := newFakeReader()
	reader.set("first-asset", srv.URL+"/frame.png")
	reader.set("last-asset", srv.URL+"/frame.png")
	sink := &fakeSink{}
	plugin := NewVideoPlugin(client, sink, reader, newFakeFileCache(), nil, "", nil, nil)

	req := execRequest(t, "task-1", map[string]any{
		"prompt": "a sunrise turning into sunset", "duration": "5", "resolution": "768P",
		"first-frame-asset-id": "first-asset", "last-frame-asset-id": "last-asset",
	})
	out, err := plugin.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Code != 0 {
		t.Fatalf("Code = %d, Message = %q, want success", out.Code, out.Message)
	}
	if gotRatio != "adaptive" {
		t.Errorf("ratio sent upstream = %q, want adaptive (i2va forces it)", gotRatio)
	}
	if len(gotContent) != 3 || gotContent[1].Role != "first_frame" || gotContent[2].Role != "last_frame" {
		t.Errorf("content sent upstream = %+v, want [text, first_frame, last_frame]", gotContent)
	}
	if len(sink.materialized) != 1 {
		t.Fatalf("sink got %d MaterializeBytes calls, want 1", len(sink.materialized))
	}
	if sink.materialized[0].Meta["mode"] != "i2va" {
		t.Errorf("materialized meta mode = %v, want i2va", sink.materialized[0].Meta["mode"])
	}
}

// servePNG is buildContent/refItem's minimal fixture — refItem only needs
// something reader.PublicURL can hand back and uploadOrGetCached can
// download, real image bytes are incidental.
func servePNG(t *testing.T) (url string, closeFn func()) {
	t.Helper()
	mux, srv, closeSrv := newImageMux(t)
	mux.HandleFunc("/asset.bin", func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte("fake-bytes"))
	})
	return srv.URL + "/asset.bin", closeSrv
}
