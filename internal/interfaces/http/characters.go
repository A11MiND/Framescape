package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"

	"aigc-platform/internal/infra/persistence"
	"aigc-platform/internal/pkg/id"
)

// createCharacterRequest is F3.1: name + description + 1-3 reference images
// + a fixed seed (the actual consistency lever, PRD §3.1).
type createCharacterRequest struct {
	Name        string   `json:"name" binding:"required"`
	Description string   `json:"description"`
	RefAssetIDs []string `json:"ref_asset_ids" binding:"required,min=1,max=3"`
	Seed        int64    `json:"seed" binding:"required"`
}

func (s *Server) handleCreateCharacter(c *gin.Context) {
	var req createCharacterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errBody("bad_request", err.Error()))
		return
	}
	refJSON, err := json.Marshal(req.RefAssetIDs)
	if err != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "marshal ref_asset_ids"))
		return
	}
	row := persistence.Character{
		BizID:       id.New(),
		UserID:      userID(c),
		Name:        req.Name,
		Description: req.Description,
		RefAssetIDs: refJSON,
		Seed:        req.Seed,
	}
	if err := s.db.WithContext(c.Request.Context()).Create(&row).Error; err != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "insert character"))
		return
	}
	c.JSON(http.StatusOK, characterToJSON(row))
}

func (s *Server) handleListCharacters(c *gin.Context) {
	var rows []persistence.Character
	err := s.db.WithContext(c.Request.Context()).
		Where("user_id = ? AND deleted_at IS NULL", userID(c)).
		Order("id DESC").Find(&rows).Error
	if err != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "list characters"))
		return
	}
	out := make([]gin.H, 0, len(rows))
	for _, r := range rows {
		out = append(out, characterToJSON(r))
	}
	c.JSON(http.StatusOK, gin.H{"characters": out})
}

func characterToJSON(row persistence.Character) gin.H {
	var refs []string
	_ = json.Unmarshal(row.RefAssetIDs, &refs)
	return gin.H{
		"biz_id":        row.BizID,
		"name":          row.Name,
		"description":   row.Description,
		"ref_asset_ids": refs,
		"seed":          row.Seed,
	}
}
