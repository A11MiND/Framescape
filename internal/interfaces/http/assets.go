package httpapi

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"aigc-platform/internal/infra/persistence"
	"aigc-platform/internal/pkg/id"
)

// handleGetAsset is the W1 subset of F2.4/F2.5 (full asset library — filters,
// pagination, "以此再生成" — lands in W3+). For now it exists so the Studio
// page can render what the mock/minimax executor actually produced instead
// of a client-side placeholder.
func (s *Server) handleGetAsset(c *gin.Context) {
	var a persistence.Asset
	if err := s.db.Where("biz_id = ? AND user_id = ?", c.Param("bizID"), userID(c)).First(&a).Error; err != nil {
		c.JSON(http.StatusNotFound, errBody("not_found", "asset not found"))
		return
	}
	c.JSON(http.StatusOK, assetToJSON(a))
}

// handleListAssets is F2.4's list surface, minus the moderation/soft-delete
// filters that don't matter yet at POC scale: paged newest-first, optional
// ?type=image|video. Backs both the character-creation ref-image picker
// (F3.1 requires ref_asset_ids to already exist — there's no raw upload
// endpoint by design, see PRD §11) and the asset library page.
func (s *Server) handleListAssets(c *gin.Context) {
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "60"))
	if err != nil || limit <= 0 || limit > 200 {
		limit = 60
	}
	q := s.db.WithContext(c.Request.Context()).
		Where("user_id = ? AND deleted_at IS NULL", userID(c))
	if t := c.Query("type"); t != "" {
		q = q.Where("type = ?", t)
	}
	var rows []persistence.Asset
	if err := q.Order("id DESC").Limit(limit).Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "list assets"))
		return
	}
	out := make([]gin.H, 0, len(rows))
	for _, a := range rows {
		out = append(out, assetToJSON(a))
	}
	c.JSON(http.StatusOK, gin.H{"assets": out})
}

// handleDeleteAsset is F2.7's soft-delete: sets deleted_at rather than
// removing the row (matches this codebase's existing soft-delete columns on
// assets/characters, already respected by every read path's
// "deleted_at IS NULL" filter — handleListAssets, resolveCharacters, etc.).
// A repeat DELETE on an already-deleted or not-owned asset is a no-op 404,
// not an error, since there's nothing unsafe about calling this twice.
func (s *Server) handleDeleteAsset(c *gin.Context) {
	now := time.Now()
	res := s.db.Model(&persistence.Asset{}).
		Where("biz_id = ? AND user_id = ? AND deleted_at IS NULL", c.Param("bizID"), userID(c)).
		Update("deleted_at", now)
	if res.Error != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "delete asset"))
		return
	}
	if res.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, errBody("not_found", "asset not found"))
		return
	}
	c.Status(http.StatusNoContent)
}

// handleBatchDownloadAssets is F2.7's other half: given a set of asset
// biz_ids, streams back a single zip so the browser gets one download
// instead of N (which browsers routinely block as pop-ups anyway).
// Deliberately fetches each asset's already-public MinIO URL rather than
// needing a storage client in cmd/api — same "public_url is a stored
// column, not computed" fact assetToJSON already relies on. An asset that's
// missing, not owned, deleted, or fails to download is silently skipped
// rather than failing the whole zip — a partial archive is more useful than
// none for a "download what you can" bulk action.
func (s *Server) handleBatchDownloadAssets(c *gin.Context) {
	var req struct {
		AssetIDs []string `json:"asset_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.AssetIDs) == 0 {
		c.JSON(http.StatusBadRequest, errBody("bad_request", "asset_ids required"))
		return
	}
	if len(req.AssetIDs) > 50 {
		req.AssetIDs = req.AssetIDs[:50] // sane upper bound for a single archive
	}

	var rows []persistence.Asset
	if err := s.db.Where("biz_id IN ? AND user_id = ? AND deleted_at IS NULL", req.AssetIDs, userID(c)).Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "load assets"))
		return
	}
	if len(rows) == 0 {
		c.JSON(http.StatusNotFound, errBody("not_found", "no matching assets"))
		return
	}

	c.Header("Content-Type", "application/zip")
	c.Header("Content-Disposition", `attachment; filename="assets.zip"`)
	zw := zip.NewWriter(c.Writer)
	defer zw.Close()

	for _, a := range rows {
		resp, err := http.Get(a.PublicURL)
		if err != nil {
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			continue
		}
		ext := path.Ext(a.PublicURL)
		if ext == "" {
			ext = ".bin"
		}
		w, err := zw.Create(a.BizID + ext)
		if err == nil {
			_, _ = io.Copy(w, resp.Body)
		}
		resp.Body.Close()
	}
}

type uploadURLRequest struct {
	Filename string `json:"filename" binding:"required"`
	Mime     string `json:"mime" binding:"required"`
}

// handleAssetUploadURL is POST /api/v1/assets/upload-url (F2.1): hands back
// a time-limited presigned PUT the browser uses directly against object
// storage — no row is written here, see handleCompleteAsset's doc for why
// nothing is persisted until the browser actually uploads to this key.
func (s *Server) handleAssetUploadURL(c *gin.Context) {
	if s.objects == nil {
		c.JSON(http.StatusServiceUnavailable, errBody("unavailable", "object storage not configured"))
		return
	}
	var req uploadURLRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errBody("bad_request", err.Error()))
		return
	}
	typ := assetTypeFromMime(req.Mime)
	if typ == "" {
		c.JSON(http.StatusBadRequest, errBody("bad_request", "mime must be image/*, video/*, or audio/*"))
		return
	}

	bizID := id.New()
	key := fmt.Sprintf("%s/%s.%s", typ, bizID, sanitizeExt(req.Filename))
	uploadURL, err := s.objects.PresignPut(c.Request.Context(), key, 15*time.Minute)
	if err != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "presign upload url"))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"biz_id":      bizID,
		"upload_url":  uploadURL,
		"storage_key": key,
	})
}

type completeAssetRequest struct {
	StorageKey string `json:"storage_key" binding:"required"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	DurationMs int    `json:"duration_ms"`
}

// handleCompleteAsset is POST /api/v1/assets/{bizID}/complete: the second
// half of F2.1's direct-upload flow. Nothing is persisted at upload-url
// time — this is what actually confirms the browser's PUT landed
// (s.objects.Stat erroring means it didn't) and creates the citable assets
// row, trusting the object store's own recorded mime/size over anything the
// client claims (width/height/duration_ms are the exception: the browser
// can read those from the file locally, the server has no way to without
// downloading and decoding it, which this pass doesn't build).
func (s *Server) handleCompleteAsset(c *gin.Context) {
	if s.objects == nil {
		c.JSON(http.StatusServiceUnavailable, errBody("unavailable", "object storage not configured"))
		return
	}
	var req completeAssetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errBody("bad_request", err.Error()))
		return
	}
	bizID := c.Param("bizID")

	typ, ok := assetTypeFromStorageKey(req.StorageKey, bizID)
	if !ok {
		c.JSON(http.StatusBadRequest, errBody("bad_request", "storage_key does not match biz_id"))
		return
	}
	info, err := s.objects.Stat(c.Request.Context(), req.StorageKey)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, errBody("upload_missing", "object not found — upload it first"))
		return
	}

	row := persistence.Asset{
		BizID:            bizID,
		UserID:           userID(c),
		Type:             typ,
		Source:           "upload",
		StorageKey:       req.StorageKey,
		PublicURL:        s.objects.PublicURLFor(req.StorageKey),
		Mime:             info.Mime,
		Width:            req.Width,
		Height:           req.Height,
		DurationMs:       req.DurationMs,
		SizeBytes:        info.SizeBytes,
		ModerationStatus: "pending",
	}
	if err := s.db.WithContext(c.Request.Context()).Create(&row).Error; err != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "insert asset"))
		return
	}
	c.JSON(http.StatusOK, assetToJSON(row))
}

func assetTypeFromMime(mime string) string {
	switch {
	case strings.HasPrefix(mime, "image/"):
		return "image"
	case strings.HasPrefix(mime, "video/"):
		return "video"
	case strings.HasPrefix(mime, "audio/"):
		return "audio"
	default:
		return ""
	}
}

// assetTypeFromStorageKey parses "<type>/<bizID>.<ext>" and confirms bizID
// matches the URL param — the one integrity check available on a
// client-reported key without proxying bytes through Go (F2.1 is
// deliberate about that), and it doubles as recovering `type` so the client
// doesn't have to send it back redundantly (handleAssetUploadURL is the
// only place that decides it, from the upload's mime).
func assetTypeFromStorageKey(key, bizID string) (string, bool) {
	typ, rest, ok := strings.Cut(key, "/")
	if !ok || typ == "" || !strings.HasPrefix(rest, bizID+".") {
		return "", false
	}
	return typ, true
}

// sanitizeExt turns a client-supplied filename into a short, safe extension
// for a MinIO object key — never trust it verbatim (path traversal, control
// characters), falling back to a generic "bin" for anything that doesn't
// look like a normal extension rather than rejecting the upload outright.
func sanitizeExt(filename string) string {
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(filename), "."))
	var b strings.Builder
	for _, r := range ext {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 || b.Len() > 8 {
		return "bin"
	}
	return b.String()
}

func assetToJSON(a persistence.Asset) gin.H {
	return gin.H{
		"biz_id":     a.BizID,
		"type":       a.Type,
		"public_url": a.PublicURL,
		"mime":       a.Mime,
		"width":      a.Width,
		"height":     a.Height,
	}
}
