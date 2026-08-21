package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"aigc-platform/internal/infra/executor/minimax"
	"aigc-platform/internal/infra/persistence"
	"aigc-platform/internal/pkg/config"
)

// newTrialTestServer wires only what handleTrialImage touches: db (unused
// by this handler directly, but NewServer requires one), redis (the rate
// limiter — skip if unreachable, this handler has no fail-open path for it),
// and a *minimax.Client pointed at a local fake server instead of the real
// MiniMax API (per this project's own convention: never hit the real
// paid API from a test).
func newTrialTestServer(t *testing.T, minimaxBaseURL string) *Server {
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
	redisClient := testRedisClient(t)
	if redisClient == nil {
		t.Skip("no local Redis available, skipping — handleTrialImage requires Redis for its rate limiting")
	}
	mm := minimax.NewClient(minimaxBaseURL, "test-api-key")
	return NewServer(db, nil, nil, nil, redisClient, testJWTSecret, mm, nil)
}

func uniqueTrialIP(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("10.%d.%d.%d:12345", rand.Intn(256), rand.Intn(256), rand.Intn(256))
}

func uniqueDeviceID(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("httptest-device-%d", rand.Int63())
}

func trialRequest(t *testing.T, r http.Handler, remoteAddr, prompt, deviceID string) *httptest.ResponseRecorder {
	t.Helper()
	rec := doJSONFromAddr(t, r, remoteAddr, struct {
		Prompt   string `json:"prompt"`
		DeviceID string `json:"device_id"`
	}{Prompt: prompt, DeviceID: deviceID})
	return rec
}

func doJSONFromAddr(t *testing.T, r http.Handler, remoteAddr string, body any) *httptest.ResponseRecorder {
	t.Helper()
	req := httptestRequest(t, http.MethodPost, "/api/v1/trial/image", body, "")
	req.RemoteAddr = remoteAddr
	return serveRequest(r, req)
}

// fakeMiniMaxImageServer's handler decides the response per call via a
// caller-supplied func, so tests that need "fails once, then succeeds"
// (the undo-the-device-claim-on-failure cases) can express that directly.
func fakeMiniMaxImageServer(handler http.HandlerFunc) *httptest.Server {
	return httptest.NewServer(handler)
}

func imageGenSuccessResponse(urls ...string) string {
	b, _ := json.Marshal(map[string]any{
		"id":   "test-id",
		"data": map[string]any{"image_urls": urls},
	})
	return string(b)
}

func TestHandleTrialImageHappyPath(t *testing.T) {
	fake := fakeMiniMaxImageServer(func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte(imageGenSuccessResponse("https://example.test/generated.png")))
	})
	defer fake.Close()
	s := newTrialTestServer(t, fake.URL)
	r := s.Router()
	ip := uniqueTrialIP(t)
	device := uniqueDeviceID(t)
	t.Cleanup(func() { s.redis.Del(context.Background(), "trial:ip:"+ip, "trial:device:"+device) })

	rec := trialRequest(t, r, ip, "a cat wearing a hat", device)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		ImageURL string `json:"image_url"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.ImageURL != "https://example.test/generated.png" {
		t.Errorf("image_url = %q, want the fake server's URL", body.ImageURL)
	}
}

func TestHandleTrialImageMissingFields(t *testing.T) {
	s := newTrialTestServer(t, "http://unused.invalid")
	r := s.Router()
	ip := uniqueTrialIP(t)

	rec := trialRequest(t, r, ip, "", uniqueDeviceID(t))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing prompt: status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	rec = trialRequest(t, r, ip, "a cat", "")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing device_id: status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandleTrialImageDeviceAlreadyUsed(t *testing.T) {
	fake := fakeMiniMaxImageServer(func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte(imageGenSuccessResponse("https://example.test/generated.png")))
	})
	defer fake.Close()
	s := newTrialTestServer(t, fake.URL)
	r := s.Router()
	ip := uniqueTrialIP(t)
	device := uniqueDeviceID(t)
	t.Cleanup(func() { s.redis.Del(context.Background(), "trial:ip:"+ip, "trial:device:"+device) })

	rec := trialRequest(t, r, ip, "first try", device)
	if rec.Code != http.StatusOK {
		t.Fatalf("first call: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	rec = trialRequest(t, r, ip, "second try, same device", device)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("second call, same device: status = %d, want %d, body = %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

func TestHandleTrialImageIPRateLimit(t *testing.T) {
	fake := fakeMiniMaxImageServer(func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte(imageGenSuccessResponse("https://example.test/generated.png")))
	})
	defer fake.Close()
	s := newTrialTestServer(t, fake.URL)
	r := s.Router()
	ip := uniqueTrialIP(t)
	var devices []string
	t.Cleanup(func() {
		keys := []string{"trial:ip:" + ip}
		for _, d := range devices {
			keys = append(keys, "trial:device:"+d)
		}
		s.redis.Del(context.Background(), keys...)
	})

	// trialIPMax=3 distinct devices from the same IP succeed...
	for i := 0; i < trialIPMax; i++ {
		device := uniqueDeviceID(t)
		devices = append(devices, device)
		rec := trialRequest(t, r, ip, "prompt", device)
		if rec.Code != http.StatusOK {
			t.Fatalf("call %d: status = %d, want %d, body = %s", i+1, rec.Code, http.StatusOK, rec.Body.String())
		}
	}
	// ...a 4th, even with yet another fresh device_id, is IP-rate-limited.
	device := uniqueDeviceID(t)
	devices = append(devices, device)
	rec := trialRequest(t, r, ip, "prompt", device)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("call %d: status = %d, want %d, body = %s", trialIPMax+1, rec.Code, http.StatusTooManyRequests, rec.Body.String())
	}
}

// TestHandleTrialImageUpstreamFailureUndoesDeviceClaim covers the handler's
// own "a failed call shouldn't burn the visitor's one trial" comment — a
// device that failed on its first attempt must still be usable afterward.
func TestHandleTrialImageUpstreamFailureUndoesDeviceClaim(t *testing.T) {
	var calls int32
	fake := fakeMiniMaxImageServer(func(w http.ResponseWriter, req *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write([]byte(imageGenSuccessResponse("https://example.test/generated.png")))
	})
	defer fake.Close()
	s := newTrialTestServer(t, fake.URL)
	r := s.Router()
	ip := uniqueTrialIP(t)
	device := uniqueDeviceID(t)
	t.Cleanup(func() { s.redis.Del(context.Background(), "trial:ip:"+ip, "trial:device:"+device) })

	rec := trialRequest(t, r, ip, "first attempt fails upstream", device)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("first attempt: status = %d, want %d, body = %s", rec.Code, http.StatusBadGateway, rec.Body.String())
	}
	rec = trialRequest(t, r, ip, "retry after upstream failure", device)
	if rec.Code != http.StatusOK {
		t.Fatalf("retry after failure: status = %d, want %d (device claim should have been undone), body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

// TestHandleTrialImageSensitiveContentMessage covers §10.4's one
// specifically-worded rejection reason (MiniMax status_code 1026) — every
// other business rejection collapses to a generic message, so this is the
// one worth pinning exactly.
func TestHandleTrialImageSensitiveContentMessage(t *testing.T) {
	fake := fakeMiniMaxImageServer(func(w http.ResponseWriter, req *http.Request) {
		b, _ := json.Marshal(map[string]any{
			"base_resp": map[string]any{"status_code": 1026, "status_msg": "sensitive content"},
		})
		w.Write(b)
	})
	defer fake.Close()
	s := newTrialTestServer(t, fake.URL)
	r := s.Router()
	ip := uniqueTrialIP(t)
	device := uniqueDeviceID(t)
	t.Cleanup(func() { s.redis.Del(context.Background(), "trial:ip:"+ip, "trial:device:"+device) })

	rec := trialRequest(t, r, ip, "something sensitive", device)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusUnprocessableEntity, rec.Body.String())
	}
	var body struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Message != "描述涉及敏感内容，请修改后重试" {
		t.Errorf("message = %q, want the specific sensitive-content wording", body.Message)
	}
}

func TestHandleTrialImageEmptyResult(t *testing.T) {
	fake := fakeMiniMaxImageServer(func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte(imageGenSuccessResponse())) // status_code 0, but no URLs
	})
	defer fake.Close()
	s := newTrialTestServer(t, fake.URL)
	r := s.Router()
	ip := uniqueTrialIP(t)
	device := uniqueDeviceID(t)
	t.Cleanup(func() { s.redis.Del(context.Background(), "trial:ip:"+ip, "trial:device:"+device) })

	rec := trialRequest(t, r, ip, "prompt", device)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusUnprocessableEntity, rec.Body.String())
	}
}
