package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"aigc-platform/internal/domain/comic"
	"aigc-platform/internal/infra/persistence"
	"aigc-platform/internal/pkg/id"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type comicSaveRequest struct {
	Version  int            `json:"version"`
	Document comic.Document `json:"document"`
}

func comicJSON(row persistence.ComicDocument) gin.H {
	return gin.H{"biz_id": row.BizID, "version": row.Version, "document": json.RawMessage(row.Document), "updated_at": row.UpdatedAt}
}
func (s *Server) handleListComics(c *gin.Context) {
	var rows []persistence.ComicDocument
	if err := s.db.WithContext(c.Request.Context()).Select("biz_id, title, version, updated_at").Where("user_id = ?", userID(c)).Order("updated_at DESC").Limit(100).Find(&rows).Error; err != nil {
		c.JSON(500, errBody("internal", "list comics"))
		return
	}
	out := make([]gin.H, 0, len(rows))
	for _, r := range rows {
		out = append(out, gin.H{"biz_id": r.BizID, "title": r.Title, "version": r.Version, "updated_at": r.UpdatedAt})
	}
	c.JSON(200, gin.H{"comics": out})
}
func (s *Server) handleGetComic(c *gin.Context) {
	var row persistence.ComicDocument
	err := s.db.WithContext(c.Request.Context()).Where("biz_id = ? AND user_id = ?", c.Param("bizID"), userID(c)).First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(404, errBody("not_found", "comic not found"))
		} else {
			c.JSON(500, errBody("internal", "read comic"))
		}
		return
	}
	c.JSON(200, comicJSON(row))
}
func (s *Server) handleSaveComic(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2<<20)
	var req comicSaveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, errBody("bad_request", "invalid or oversized comic document"))
		return
	}
	if err := req.Document.Validate(); err != nil {
		c.JSON(422, errBody("invalid_comic", err.Error()))
		return
	}
	ctx := c.Request.Context()
	uid := userID(c)
	ids := req.Document.AssetIDs()
	if len(ids) > 0 {
		var count int64
		if err := s.db.WithContext(ctx).Model(&persistence.Asset{}).Where("biz_id IN ? AND user_id = ? AND type = ? AND mime IN ? AND deleted_at IS NULL AND public_url <> ''", ids, uid, "image", []string{"image/png", "image/jpeg", "image/webp"}).Count(&count).Error; err != nil {
			c.JSON(500, errBody("internal", "validate comic assets"))
			return
		}
		if count != int64(len(ids)) {
			c.JSON(422, errBody("invalid_asset", "comic contains unavailable images"))
			return
		}
	}
	if req.Document.Pending != nil {
		var count int64
		if err := s.db.WithContext(ctx).Model(&persistence.Job{}).Where("biz_id = ? AND user_id = ? AND workflow_name = ? AND deleted_at IS NULL", req.Document.Pending.JobID, uid, "image.comic4").Count(&count).Error; err != nil {
			c.JSON(500, errBody("internal", "validate job"))
			return
		}
		if count != 1 {
			c.JSON(422, errBody("invalid_job", "generation unavailable"))
			return
		}
	}
	data, _ := json.Marshal(req.Document)
	bizID := c.Param("bizID")
	if bizID == "" {
		if req.Version != 0 {
			c.JSON(409, errBody("version_conflict", "new comic version must be zero"))
			return
		}
		row := persistence.ComicDocument{BizID: id.New(), UserID: uid, Title: req.Document.Title, Document: data, Version: 1}
		if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
			c.JSON(500, errBody("internal", "create comic"))
			return
		}
		c.JSON(200, comicJSON(row))
		return
	}
	var existing persistence.ComicDocument
	if err := s.db.WithContext(ctx).Where("biz_id = ? AND user_id = ?", bizID, uid).First(&existing).Error; err != nil {
		c.JSON(404, errBody("not_found", "comic not found"))
		return
	}
	res := s.db.WithContext(ctx).Model(&persistence.ComicDocument{}).Where("id = ? AND version = ?", existing.ID, req.Version).Updates(map[string]any{"title": req.Document.Title, "document": data, "version": req.Version + 1})
	if res.Error != nil {
		c.JSON(500, errBody("internal", "save comic"))
		return
	}
	if res.RowsAffected != 1 {
		c.JSON(409, errBody("version_conflict", "this comic was edited elsewhere; reload or save a separate copy"))
		return
	}
	// Return this write's version, not a later concurrent writer's document.
	c.JSON(200, gin.H{"biz_id": bizID, "version": req.Version + 1, "document": req.Document})
}
