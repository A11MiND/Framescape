// Package media produces derived files for assets, currently the small
// preview image grids load instead of the original.
package media

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"os/exec"
	"time"

	_ "image/gif"
	_ "image/png"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"

	"aigc-platform/internal/infra/orchestrator"
	"aigc-platform/internal/infra/storage"
)

// TaskThumbnail generates one asset's preview.
const TaskThumbnail = "asset:thumbnail"

// ThumbEdge is the longest edge of a preview, in pixels.
const ThumbEdge = 512

// ThumbnailTask builds the follow-up task for an asset.
func ThumbnailTask(assetBizID string) orchestrator.Task {
	payload, _ := json.Marshal(map[string]string{"asset_id": assetBizID})
	return orchestrator.Task{Kind: TaskThumbnail, Queue: orchestrator.QueueSystem, Payload: payload, UniqueID: "thumb:" + assetBizID, Timeout: 2 * time.Minute}
}

type Thumbnailer struct {
	db      *sql.DB
	objects *storage.Store
	http    *http.Client
}

func NewThumbnailer(db *sql.DB, objects *storage.Store) *Thumbnailer {
	return &Thumbnailer{db: db, objects: objects, http: &http.Client{Timeout: time.Minute}}
}

// Handle creates the preview for the asset in payload. Assets that already
// have one, are gone, or cannot be decoded are skipped rather than retried.
func (t *Thumbnailer) Handle(ctx context.Context, payload []byte) error {
	var p struct {
		AssetID string `json:"asset_id"`
	}
	if json.Unmarshal(payload, &p) != nil || p.AssetID == "" {
		return nil
	}
	var kind, url, existing string
	err := t.db.QueryRowContext(ctx, `SELECT type, public_url, thumb_url FROM assets WHERE biz_id = ? AND deleted_at IS NULL`, p.AssetID).Scan(&kind, &url, &existing)
	if err == sql.ErrNoRows || existing != "" || url == "" {
		return nil
	}
	if err != nil {
		return err
	}
	var thumb []byte
	switch kind {
	case "image":
		thumb, err = t.imageThumb(ctx, url)
	case "video":
		thumb, err = videoThumb(ctx, url)
	default:
		return nil
	}
	if err != nil {
		return nil
	}
	key := "thumbs/" + p.AssetID + ".jpg"
	publicURL, err := t.objects.Put(ctx, key, bytes.NewReader(thumb), int64(len(thumb)), "image/jpeg")
	if err != nil {
		return err
	}
	_, err = t.db.ExecContext(ctx, `UPDATE assets SET thumb_key = ?, thumb_url = ? WHERE biz_id = ?`, key, publicURL, p.AssetID)
	return err
}

func (t *Thumbnailer) imageThumb(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := t.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}
	src, _, err := image.Decode(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	return encode(Resize(src, ThumbEdge))
}

// Resize scales src so its longest edge is at most edge pixels.
func Resize(src image.Image, edge int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= edge && h <= edge {
		return src
	}
	if w >= h {
		h, w = max(1, h*edge/w), edge
	} else {
		w, h = max(1, w*edge/h), edge
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Src, nil)
	return dst
}

func encode(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 82})
	return buf.Bytes(), err
}

// videoThumb takes a frame shortly after the start, scaled down.
func videoThumb(ctx context.Context, url string) ([]byte, error) {
	cctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	cmd := exec.CommandContext(cctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-ss", "0.3", "-i", url,
		"-frames:v", "1", "-vf", fmt.Sprintf("scale='if(gt(iw,ih),min(%d,iw),-2)':'if(gt(iw,ih),-2,min(%d,ih))'", ThumbEdge, ThumbEdge),
		"-f", "image2", "-c:v", "mjpeg", "-q:v", "4", "pipe:1")
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		return nil, fmt.Errorf("extract frame: %v", err)
	}
	return out, nil
}
