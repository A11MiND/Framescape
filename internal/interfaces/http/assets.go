package httpapi

import (
	"archive/zip"
	"io"
	"net/http"
	"path"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"aigc-platform/internal/infra/persistence"
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
