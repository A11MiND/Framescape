package httpapi

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"aigc-platform/internal/application/upkeep"
	"aigc-platform/internal/infra/persistence"
	"aigc-platform/internal/pkg/id"
)

// handleGetAsset is F2.5's detail read: the full record, not the list
// projection assetToJSON gives everywhere else — see assetDetailJSON's own
// doc for what the difference is and why it's only worth paying for here.
func (s *Server) handleGetAsset(c *gin.Context) {
	// Every sibling single-resource handler (handleUpdateAsset,
	// handleDeleteAsset, and the character/project equivalents) filters
	// deleted_at IS NULL — this one didn't, an isolated oversight found
	// live: a soft-deleted asset stayed fully viewable, downloadable, and
	// re-deletable via its own direct URL indefinitely after F2.7's
	// "delete" supposedly removed it.
	var a persistence.Asset
	if err := s.db.Where("biz_id = ? AND user_id = ? AND deleted_at IS NULL", c.Param("bizID"), userID(c)).First(&a).Error; err != nil {
		c.JSON(http.StatusNotFound, errBody("not_found", "asset not found"))
		return
	}
	c.JSON(http.StatusOK, s.assetDetailJSON(c.Request.Context(), a))
}

// handleListAssets is F2.4's list surface, minus the moderation/soft-delete
// filters that don't matter yet at POC scale: paged newest-first, optional
// ?type=image|video and ?project_id=<biz_id> (§the composer-blueprint
// artifact's "資產庫的專案篩選" gap — assets.idx_project has existed since
// 00001, unused until projects.go's CRUD gave it something to reference).
// Backs both the character-creation ref-image picker (F3.1 requires
// ref_asset_ids to already exist — there's no raw upload endpoint by
// design, see PRD §11) and the asset library page.
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
	if pid := c.Query("project_id"); pid != "" {
		projectID, ok := s.resolveProjectID(c.Request.Context(), userID(c), pid)
		if !ok {
			c.JSON(http.StatusNotFound, errBody("not_found", "project not found"))
			return
		}
		q = q.Where("project_id = ?", projectID)
	}
	// Full-text search (?q=): assets carry no title of their own, so the
	// only searchable text is the generation prompt every image/video
	// executor already writes to meta.prompt (image.go/video.go — uploaded
	// assets simply never match, which is correct, they have no prompt).
	if term := c.Query("q"); term != "" {
		q = q.Where("JSON_UNQUOTE(JSON_EXTRACT(meta, '$.prompt')) LIKE ?", "%"+term+"%")
	}
	var rows []persistence.Asset
	if err := q.Order("id DESC").Limit(limit).Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "list assets"))
		return
	}

	// Batch-resolve project_id -> biz_id once for the whole page rather
	// than a query per row — same reasoning as handleGetJob's projByKey.
	projectIDs := make([]uint64, 0)
	seen := map[uint64]bool{}
	for _, a := range rows {
		if a.ProjectID != nil && !seen[*a.ProjectID] {
			seen[*a.ProjectID] = true
			projectIDs = append(projectIDs, *a.ProjectID)
		}
	}
	projectBizByID := make(map[uint64]string, len(projectIDs))
	if len(projectIDs) > 0 {
		var projects []persistence.Project
		_ = s.db.WithContext(c.Request.Context()).Where("id IN ?", projectIDs).Find(&projects).Error
		for _, p := range projects {
			projectBizByID[p.ID] = p.BizID
		}
	}

	out := make([]gin.H, 0, len(rows))
	for _, a := range rows {
		projectBizID := ""
		if a.ProjectID != nil {
			projectBizID = projectBizByID[*a.ProjectID]
		}
		out = append(out, assetToJSON(a, projectBizID))
	}
	c.JSON(http.StatusOK, gin.H{"assets": out})
}

// handleCommunityFeed is the community feed's read side (migration 00010):
// every user's own published assets, newest-published-first, across every
// account — deliberately the one asset list in this codebase NOT scoped to
// "WHERE user_id = caller". Only ever returns rows the owner explicitly
// flagged is_public via handleUpdateAsset, and only a minimal projection
// (no project_id, no owner identity) — a viewer here isn't the owner, so
// nothing owner-specific belongs in the response. See migration 00010's own
// doc for the moderation gap this doesn't attempt to close.
func (s *Server) handleCommunityFeed(c *gin.Context) {
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "60"))
	if err != nil || limit <= 0 || limit > 200 {
		limit = 60
	}
	var rows []persistence.Asset
	if err := s.db.WithContext(c.Request.Context()).
		Where("is_public = ? AND deleted_at IS NULL", true).
		Order("published_at DESC").Limit(limit).Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "list community feed"))
		return
	}

	out := make([]gin.H, 0, len(rows))
	for _, a := range rows {
		var meta map[string]any
		if len(a.Meta) > 0 {
			_ = json.Unmarshal(a.Meta, &meta)
		}
		out = append(out, gin.H{
			"biz_id":         a.BizID,
			"type":           a.Type,
			"public_url":     a.PublicURL,
			"width":          a.Width,
			"height":         a.Height,
			"resolution_tag": a.ResolutionTag,
			"published_at":   a.PublishedAt,
			// prompt only — meta can carry seed/model/minimax_task_id too,
			// none of which mean anything to another viewer.
			"prompt": meta["prompt"],
		})
	}
	c.JSON(http.StatusOK, gin.H{"assets": out})
}

// resolveProjectID turns a project biz_id into its numeric id, scoped to
// userID so one user can't file an asset under another's project — same
// ownership-check shape as resolveCharacters in jobsvc.
func (s *Server) resolveProjectID(ctx context.Context, userID uint64, bizID string) (uint64, bool) {
	var row persistence.Project
	err := s.db.WithContext(ctx).
		Where("biz_id = ? AND user_id = ? AND deleted_at IS NULL", bizID, userID).
		First(&row).Error
	return row.ID, err == nil
}

// updateAssetRequest is currently just project assignment (§the "資產庫的
// 專案篩選" gap's other half — filtering needs somewhere to assign an asset
// TO first). ProjectID is a pointer-to-pointer-shaped choice via a
// separate Clear flag: "" JSON body has no way to distinguish "omitted"
// from "explicitly unassign", so Clear does that explicitly.
type updateAssetRequest struct {
	ProjectID *string `json:"project_id"`
	Clear     bool    `json:"clear_project"`
	// IsPublic backs the community feed (migration 00010) — publishing/
	// unpublishing an asset the caller already owns, never a way to touch
	// anyone else's (the WHERE clause below still filters on user_id, same
	// as every other field this handler can change).
	IsPublic *bool `json:"is_public"`
}

func (s *Server) handleUpdateAsset(c *gin.Context) {
	var req updateAssetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errBody("bad_request", err.Error()))
		return
	}

	updates := map[string]any{}
	if req.Clear {
		updates["project_id"] = nil
	} else if req.ProjectID != nil {
		resolved, ok := s.resolveProjectID(c.Request.Context(), userID(c), *req.ProjectID)
		if !ok {
			c.JSON(http.StatusNotFound, errBody("not_found", "project not found"))
			return
		}
		updates["project_id"] = resolved
	}
	if req.IsPublic != nil {
		updates["is_public"] = *req.IsPublic
		if *req.IsPublic {
			updates["published_at"] = time.Now()
		} else {
			updates["published_at"] = nil
		}
	}
	if len(updates) == 0 {
		c.JSON(http.StatusBadRequest, errBody("bad_request", "no fields to update"))
		return
	}

	// Existence is checked by the reload below, not by RowsAffected here —
	// see handleUpdateCharacter's identical comment for why (MySQL's
	// default driver reports RowsAffected as rows changed, not matched, so
	// re-assigning an asset to the project it's already in would otherwise
	// false-404 — the exact case that surfaced this while UI-testing this
	// handler live).
	res := s.db.WithContext(c.Request.Context()).Model(&persistence.Asset{}).
		Where("biz_id = ? AND user_id = ? AND deleted_at IS NULL", c.Param("bizID"), userID(c)).
		Updates(updates)
	if res.Error != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "update asset"))
		return
	}

	var exists int64
	if err := s.db.WithContext(c.Request.Context()).Model(&persistence.Asset{}).
		Where("biz_id = ? AND user_id = ? AND deleted_at IS NULL", c.Param("bizID"), userID(c)).
		Count(&exists).Error; err != nil || exists == 0 {
		c.JSON(http.StatusNotFound, errBody("not_found", "asset not found"))
		return
	}
	c.Status(http.StatusOK)
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

// handleListTrash is the recycle bin's read side: every asset the caller
// has soft-deleted (handleDeleteAsset) that autoPurgeTrash hasn't caught up
// with yet, newest-deleted-first, each annotated with days left before
// that happens.
func (s *Server) handleListTrash(c *gin.Context) {
	var rows []persistence.Asset
	if err := s.db.WithContext(c.Request.Context()).
		Where("user_id = ? AND deleted_at IS NOT NULL", userID(c)).
		Order("deleted_at DESC").Limit(200).Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "list trash"))
		return
	}
	out := make([]gin.H, 0, len(rows))
	for _, a := range rows {
		j := assetToJSON(a, "")
		delete(j, "project_id")
		j["deleted_at"] = a.DeletedAt
		if a.DeletedAt != nil {
			purgeAt := a.DeletedAt.AddDate(0, 0, upkeep.TrashRetentionDays)
			daysLeft := int(time.Until(purgeAt).Hours() / 24)
			if daysLeft < 0 {
				daysLeft = 0
			}
			j["days_until_purge"] = daysLeft
		}
		out = append(out, j)
	}
	c.JSON(http.StatusOK, gin.H{"assets": out})
}

// handleRestoreAsset undoes a soft-delete — the only way trash rows leave
// this state other than autoPurgeTrash actually catching up with them.
func (s *Server) handleRestoreAsset(c *gin.Context) {
	res := s.db.WithContext(c.Request.Context()).Model(&persistence.Asset{}).
		Where("biz_id = ? AND user_id = ? AND deleted_at IS NOT NULL", c.Param("bizID"), userID(c)).
		Update("deleted_at", nil)
	if res.Error != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "restore asset"))
		return
	}
	if res.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, errBody("not_found", "asset not found in trash"))
		return
	}
	c.Status(http.StatusNoContent)
}

// handleEmptyTrash is the user-triggered "一键倾倒" (empty trash now)
// counterpart to upkeep.Runner.autoPurgeTrash: same storage-object-then-row
// hard-delete order (never orphan a storage object by deleting its row
// first), just scoped to the caller's own trash and with no
// TrashRetentionDays cutoff — every one of their own already-soft-deleted
// assets, not just the ones old enough for the automatic sweep. This is a
// real, permanent, unrecoverable delete; the frontend's own doc covers why
// that's fine here (the user explicitly asked to empty trash, not casually
// clicked something).
func (s *Server) handleEmptyTrash(c *gin.Context) {
	ctx := c.Request.Context()
	var rows []persistence.Asset
	if err := s.db.WithContext(ctx).Where("user_id = ? AND deleted_at IS NOT NULL", userID(c)).Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "list trash"))
		return
	}
	purged := 0
	for _, a := range rows {
		if a.StorageKey != "" {
			if err := s.objects.Delete(ctx, a.StorageKey); err != nil {
				continue // leave the row for a later purge rather than orphan the object
			}
		}
		if a.ThumbKey != "" {
			if err := s.objects.Delete(ctx, a.ThumbKey); err != nil {
				continue
			}
		}
		if err := s.db.WithContext(ctx).Delete(&persistence.Asset{}, a.ID).Error; err != nil {
			continue
		}
		purged++
	}
	c.JSON(http.StatusOK, gin.H{"purged": purged})
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
	c.JSON(http.StatusOK, assetToJSON(row, "")) // newly uploaded assets are never pre-assigned to a project
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

// assetToJSON is the list-view projection (handleListAssets, and the two
// upload handlers' own response) — every field here is already loaded on
// the row, no extra query. resolution_tag has existed on the assets table
// since the very first migration but had no HTTP field until now — see
// §19.4.7's "视频资产卡片右上角常驻显示 768P/2K 标签".
// projectBizID is the caller-resolved biz_id for a.ProjectID (empty if
// unassigned) — resolved by the caller, not here, so handleListAssets can
// batch it once per page instead of once per row.
func assetToJSON(a persistence.Asset, projectBizID string) gin.H {
	return gin.H{
		"biz_id":         a.BizID,
		"type":           a.Type,
		"public_url":     a.PublicURL,
		"mime":           a.Mime,
		"width":          a.Width,
		"height":         a.Height,
		"resolution_tag": a.ResolutionTag,
		"created_at":     a.CreatedAt,
		"project_id":     projectBizID,
		"is_public":      a.IsPublic,
	}
}

// assetDetailJSON is handleGetAsset's richer single-asset projection
// (F2.5): everything assetToJSON has, plus the generation params every
// executor already writes to assets.meta (model/prompt/seed — see e.g.
// minimax/image.go's Materialize call) and, when this asset came from a
// job rather than an upload, that job's biz_id so the detail page can link
// back to it and offer "以此再生成" (§19.0②). The job lookup is a second
// query (through job_nodes, the only table that maps a task_run_id to a
// job_id) — worth it here since this is a single-row fetch, not something
// handleListAssets should ever pay N times over.
func (s *Server) assetDetailJSON(ctx context.Context, a persistence.Asset) gin.H {
	projectBizID := ""
	if a.ProjectID != nil {
		_ = s.db.WithContext(ctx).Model(&persistence.Project{}).
			Select("biz_id").Where("id = ?", *a.ProjectID).Scan(&projectBizID).Error
	}
	out := assetToJSON(a, projectBizID)
	out["source"] = a.Source

	var meta map[string]any
	if len(a.Meta) > 0 {
		_ = json.Unmarshal(a.Meta, &meta)
	}
	out["meta"] = meta

	jobBizID := ""
	if a.FromTaskRunID != "" {
		_ = s.db.WithContext(ctx).Table("job_nodes").
			Select("jobs.biz_id").
			Joins("JOIN jobs ON jobs.id = job_nodes.job_id").
			Where("job_nodes.task_run_id = ?", a.FromTaskRunID).
			Limit(1).
			Scan(&jobBizID).Error
	}
	out["job_biz_id"] = jobBizID

	// provider_files (migration 00003) is the MiniMax file_id cache that
	// avoids re-uploading the same asset on every reference use (§9.2/F3.4)
	// — it's existed since W4 but never had a read path of its own, so the
	// UI had no way to show whether a given asset is currently cached. A
	// missing row just means "never uploaded to MiniMax," not an error.
	var pf persistence.ProviderFile
	cached := s.db.WithContext(ctx).
		Where("asset_id = ? AND provider_code = ?", a.ID, "minimax").
		Order("id DESC").First(&pf).Error == nil
	cacheInfo := gin.H{"cached": cached}
	if cached {
		cacheInfo["expire_at"] = pf.ExpireAt
		cacheInfo["expired"] = pf.ExpireAt != nil && pf.ExpireAt.Before(time.Now())
	}
	out["provider_cache"] = cacheInfo
	return out
}
