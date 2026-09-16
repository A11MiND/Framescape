package gemini

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/gif" // decoder registration for image.DecodeConfig
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// imageDimensions/bytesReader/parseUserID/extForContentType/downloadBytes
// mirror internal/infra/executor/minimax/util.go and file_upload.go's own
// copies exactly — small enough, and specific enough to each provider's own
// materialize step, that a shared package would be pure indirection.

func imageDimensions(data []byte) (width, height int) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }

func parseUserID(s string) uint64 {
	v, _ := strconv.ParseUint(s, 10, 64)
	return v
}

func extForContentType(contentType string) string {
	ct := strings.ToLower(strings.SplitN(contentType, ";", 2)[0])
	switch ct {
	case "image/png":
		return "png"
	case "image/webp":
		return "webp"
	case "image/gif":
		return "gif"
	default:
		return "jpg"
	}
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
