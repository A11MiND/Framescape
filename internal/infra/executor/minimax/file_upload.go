package minimax

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/BabySid/aether/executor"
	"github.com/BabySid/aether/model"

	"aigc-platform/internal/infra/executor/assetstore"
)

// FileCache is the provider_files read/write capability (PRD §9.2, F3.4):
// "同一素材第二次使用不重复上传". Defined here (consumer side) and
// implemented in internal/infra/persistence, same pattern as
// assetstore.Sink/Reader.
type FileCache interface {
	Get(ctx context.Context, assetBizID string) (fileID string, ok bool, err error)
	Put(ctx context.Context, assetBizID string, fileID string, purpose string, expireAt time.Time) error
}

const defaultUploadPurpose = "video_generation_input"

// fileExpiryDays matches MiniMax's stated retention for video_generation_input
// uploads (7 days) — provider_files.expire_at lets a cron job (§11.4, W7)
// clean up rows for files MiniMax has already discarded.
const fileExpiryDays = 7

type FileUploadConfig struct {
	AssetID string `json:"asset-id"`
	Purpose string `json:"purpose"`
}

// FileUploadPlugin is minimax.file.upload (F3.4): uploads a reference asset
// once and caches its file_id so every later generation call that needs the
// same asset (a character's reference image reused across many shots) reuses
// mm_file://{file_id} instead of re-uploading.
type FileUploadPlugin struct {
	client *Client
	reader assetstore.Reader
	cache  FileCache
}

func NewFileUploadPlugin(client *Client, reader assetstore.Reader, cache FileCache) *FileUploadPlugin {
	return &FileUploadPlugin{client: client, reader: reader, cache: cache}
}

func (p *FileUploadPlugin) Type() string { return "minimax.file.upload" }

func (p *FileUploadPlugin) Schema() model.ExecutorSchema {
	return executor.SchemaOf[FileUploadConfig, executor.DynamicOutputs](
		"minimax.file.upload", "1.0", "Upload (once, cached) an asset to MiniMax for mm_file:// reuse (F3.4)",
	)
}

func (p *FileUploadPlugin) Execute(ctx context.Context, req *executor.ExecuteRequest) (*model.ExecOutputs, error) {
	var cfg FileUploadConfig
	if err := executor.BindInputs(req.Inputs, &cfg); err != nil {
		return nil, fmt.Errorf("bind minimax.file.upload inputs: %w", err)
	}
	if cfg.Purpose == "" {
		cfg.Purpose = defaultUploadPurpose
	}

	fileID, cached, err := uploadOrGetCached(ctx, p.client, p.reader, p.cache, cfg.AssetID, cfg.Purpose)
	if err != nil {
		return &model.ExecOutputs{Code: model.ExecCodeError, Message: err.Error()}, nil
	}
	return executor.OutputFrom(struct {
		FileID string `json:"file-id"`
		Cached bool   `json:"cached"`
	}{FileID: fileID, Cached: cached})
}

// uploadOrGetCached resolves an asset to a MiniMax file_id, uploading only
// on a cache miss. Shared by FileUploadPlugin (the standalone F3.4
// executor) and VideoPlugin (which needs the same mm_file:// resolution for
// every image/video/audio reference it sends — our own object storage isn't
// internet-reachable, so MiniMax can never fetch a URL from us; pushing the
// bytes via this upload is the only way references work at all, see
// docs/aether-validation-report.md's W5 addendum).
func uploadOrGetCached(ctx context.Context, client *Client, reader assetstore.Reader, cache FileCache, assetBizID, purpose string) (fileID string, cached bool, err error) {
	if id, ok, err := cache.Get(ctx, assetBizID); err == nil && ok {
		return id, true, nil
	}

	url, err := reader.PublicURL(ctx, assetBizID)
	if err != nil {
		return "", false, fmt.Errorf("look up asset %s: %w", assetBizID, err)
	}
	data, err := downloadBytes(ctx, url)
	if err != nil {
		return "", false, fmt.Errorf("download asset %s: %w", assetBizID, err)
	}

	resp, err := client.UploadFile(ctx, purpose, assetBizID+extFromURL(url), data)
	if err != nil {
		return "", false, fmt.Errorf("upload to minimax: %w", err)
	}
	if resp.BaseResp.StatusCode != 0 {
		return "", false, fmt.Errorf("minimax upload error %d: %s", resp.BaseResp.StatusCode, resp.BaseResp.StatusMsg)
	}

	id := fmt.Sprintf("%d", resp.File.FileID)
	expireAt := time.Now().Add(fileExpiryDays * 24 * time.Hour)
	if err := cache.Put(ctx, assetBizID, id, purpose, expireAt); err != nil {
		// Upload succeeded; failing to cache just means a future call
		// re-uploads instead of reusing — not worth failing the task over.
		_ = err
	}
	return id, false, nil
}

func downloadBytes(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func extFromURL(url string) string {
	if i := strings.LastIndex(url, "."); i >= 0 && i > strings.LastIndex(url, "/") {
		return url[i:]
	}
	return ".jpg"
}

var _ executor.Plugin = (*FileUploadPlugin)(nil)
