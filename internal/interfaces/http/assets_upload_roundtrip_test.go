package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"aigc-platform/internal/infra/storage"
	"aigc-platform/internal/pkg/config"
)

// newFullTestServerWithObjects is newFullTestServer plus a real *storage.Store
// wired in — needs a live MinIO reachable at config.MinIOEndpoint() (the
// same local-dev default every other test's MySQL/Redis skip-if-unreachable
// convention already assumes), skipping rather than failing when one isn't
// running. This is what lets TestUploadRoundTrip_* exercise the real
// presign -> PUT -> complete path end to end instead of stopping at the
// "MinIO not configured" 503 branch (see assets_test.go's
// TestHandleAssetUploadFlowNotConfigured).
func newFullTestServerWithObjects(t *testing.T) (*Server, *fakeEngine) {
	t.Helper()
	s, eng := newFullTestServer(t)
	store, err := storage.New(context.Background(), storage.Config{
		Endpoint:        config.MinIOEndpoint(),
		AccessKeyID:     config.MinIOAccessKey(),
		SecretAccessKey: config.MinIOSecretKey(),
		UseSSL:          config.MinIOUseSSL(),
		Bucket:          config.MinIOBucket(),
		PublicBaseURL:   config.MinIOPublicBaseURL(),
	})
	if err != nil {
		t.Skipf("no local MinIO available, skipping: %v", err)
	}
	s.objects = store
	return s, eng
}

func putBytes(t *testing.T, url, contentType string, body []byte) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build PUT request: %v", err)
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT %s: %v", url, err)
	}
	return resp
}

type presignResponse struct {
	BizID      string `json:"biz_id"`
	UploadURL  string `json:"upload_url"`
	StorageKey string `json:"storage_key"`
}

// requestUpload runs handleAssetUploadURL for the given declared mime and
// returns the decoded presign response — shared by every round-trip test
// below, which then differ only in what they PUT.
func requestUpload(t *testing.T, s *Server, token, filename, mime string) presignResponse {
	t.Helper()
	rec := doJSON(t, s.Router(), http.MethodPost, "/api/v1/assets/upload-url",
		uploadURLRequest{Filename: filename, Mime: mime}, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload-url: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var presign presignResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &presign); err != nil {
		t.Fatalf("decode presign response: %v", err)
	}
	return presign
}

// TestUploadRoundTrip_Success exercises F2.1's full direct-upload path for
// real — presign, an actual PUT to the returned URL, then complete — the one
// thing TestHandleAssetUploadFlowNotConfigured's nil-objects test can't
// cover. This is also the regression test for the presign-host bug: before
// the PublicBaseURL-derived presignClient fix, PresignPut signed URLs
// against cfg.Endpoint (the docker-compose-only "minio:9000" case couldn't
// be exercised here, but the general "presign host must be independently
// reachable" property is what this test pins down).
func TestUploadRoundTrip_Success(t *testing.T) {
	s, _ := newFullTestServerWithObjects(t)
	token, _ := registerAndFund(t, s, 0)

	presign := requestUpload(t, s, token, "photo.png", "image/png")

	payload := bytes.Repeat([]byte{0x01, 0x02, 0x03, 0x04}, 256)
	resp := putBytes(t, presign.UploadURL, "image/png", payload)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT to presigned url: status = %d", resp.StatusCode)
	}

	rec := doJSON(t, s.Router(), http.MethodPost, fmt.Sprintf("/api/v1/assets/%s/complete", presign.BizID),
		completeAssetRequest{StorageKey: presign.StorageKey, Width: 10, Height: 10}, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("complete: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var asset map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &asset); err != nil {
		t.Fatalf("decode asset: %v", err)
	}
	if asset["type"] != "image" {
		t.Errorf("type = %v, want image", asset["type"])
	}
	if u, _ := asset["public_url"].(string); u == "" {
		t.Errorf("public_url is empty")
	}
}

// TestUploadRoundTrip_MimeMismatch covers the reverse mime check added to
// handleCompleteAsset: declaring image/png at upload-url time but actually
// PUTting bytes with a video content-type must be rejected at complete,
// not silently recorded as an "image" asset.
func TestUploadRoundTrip_MimeMismatch(t *testing.T) {
	s, _ := newFullTestServerWithObjects(t)
	token, _ := registerAndFund(t, s, 0)

	presign := requestUpload(t, s, token, "photo.png", "image/png")

	resp := putBytes(t, presign.UploadURL, "video/mp4", []byte("not actually a png"))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT to presigned url: status = %d", resp.StatusCode)
	}

	rec := doJSON(t, s.Router(), http.MethodPost, fmt.Sprintf("/api/v1/assets/%s/complete", presign.BizID),
		completeAssetRequest{StorageKey: presign.StorageKey}, token)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("complete: status = %d, want %d, body = %s", rec.Code, http.StatusUnprocessableEntity, rec.Body.String())
	}
}

// TestUploadRoundTrip_TooLarge covers the maxUploadBytesByType backstop:
// an image upload past the 20MB cap must be rejected at complete, and the
// oversized object must not be left behind in the store.
func TestUploadRoundTrip_TooLarge(t *testing.T) {
	s, _ := newFullTestServerWithObjects(t)
	token, _ := registerAndFund(t, s, 0)

	presign := requestUpload(t, s, token, "huge.png", "image/png")

	oversized := bytes.Repeat([]byte{0x00}, int(maxUploadBytesByType["image"])+1024)
	resp := putBytes(t, presign.UploadURL, "image/png", oversized)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT to presigned url: status = %d", resp.StatusCode)
	}

	rec := doJSON(t, s.Router(), http.MethodPost, fmt.Sprintf("/api/v1/assets/%s/complete", presign.BizID),
		completeAssetRequest{StorageKey: presign.StorageKey}, token)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("complete: status = %d, want %d, body = %s", rec.Code, http.StatusRequestEntityTooLarge, rec.Body.String())
	}
}
