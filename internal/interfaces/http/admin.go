// Package httpapi's admin surface: usage overview, user list, and manual
// credit grants for operators. Every route here sits behind both
// requireAuth and requireAdmin (server.go) — a non-admin caller never
// reaches any handler in this file.
//
// "API usage" is scoped to what this app already tracks in jobs/
// credit_ledger (job counts by workflow/status, credits actually consumed)
// rather than raw per-request MiniMax call logs — no such log table exists
// anywhere in this codebase, and building one is a real, separate feature,
// not a rename of existing data. What's here answers "how much is this
// platform actually being used, by whom, and what is it costing" — the
// question an operator asks day to day — without inventing new tracking
// infrastructure nobody asked for yet.
package httpapi

import (
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"aigc-platform/internal/infra/persistence"
	"aigc-platform/internal/pkg/id"
)

// handleAdminOverview is GET /api/v1/admin/overview: the dashboard's top
// summary strip — total users, jobs by status, and the two credit totals
// (recharged vs. consumed) that between them say how much real MiniMax
// spend this platform's credits currently represent (creditsvc's own
// CreditsFromYuan doc: credits are a fixed multiple of yuan cost, so a sum
// of committed amounts is a real, if indirect, usage/cost signal).
func (s *Server) handleAdminOverview(c *gin.Context) {
	ctx := c.Request.Context()

	var userCount int64
	s.db.WithContext(ctx).Model(&persistence.User{}).Count(&userCount)

	var jobStatusRows []struct {
		Status string
		N      int64
	}
	s.db.WithContext(ctx).Model(&persistence.Job{}).
		Select("status, COUNT(*) as n").Group("status").Scan(&jobStatusRows)
	jobsByStatus := map[string]int64{}
	for _, r := range jobStatusRows {
		jobsByStatus[r.Status] = r.N
	}

	var totalRecharged, totalConsumed int64
	s.db.WithContext(ctx).Model(&persistence.CreditLedger{}).
		Where("direction = ?", "recharge").
		Select("COALESCE(SUM(amount), 0)").Scan(&totalRecharged)
	// commit rows carry amount = -actual (creditsvc.Commit's own doc) — negate
	// back to a positive "credits consumed" figure.
	var negConsumed int64
	s.db.WithContext(ctx).Model(&persistence.CreditLedger{}).
		Where("direction = ?", "commit").
		Select("COALESCE(SUM(amount), 0)").Scan(&negConsumed)
	totalConsumed = -negConsumed

	c.JSON(http.StatusOK, gin.H{
		"user_count":        userCount,
		"jobs_by_status":    jobsByStatus,
		"credits_recharged": totalRecharged,
		"credits_consumed":  totalConsumed,
	})
}

// adminUserRow is one row of GET /api/v1/admin/users — a user plus the
// account/usage figures an operator actually needs to decide whether to
// grant more credits or investigate, joined here rather than making the
// frontend fan out N+1 requests.
type adminUserRow struct {
	BizID        string    `json:"biz_id"`
	Email        *string   `json:"email"`
	Phone        *string   `json:"phone"`
	IsAdmin      bool      `json:"is_admin"`
	Balance      int       `json:"balance"`
	Held         int       `json:"held"`
	CreditsSpent int       `json:"credits_spent"`
	JobCount     int       `json:"job_count"`
	CreatedAt    time.Time `json:"created_at"`
}

// handleAdminListUsers is GET /api/v1/admin/users?q=&limit=&cursor=:
// newest-first by id, same cursor-pagination shape as handleListJobs/
// handleCreditsLedger elsewhere in this package. q, when set, matches
// email/phone by substring (LIKE) — this platform's user base is small
// enough (POC scale, same assumption upkeep.go/asset_likes.sql already
// make) that a plain LIKE scan needs no dedicated search index.
func (s *Server) handleAdminListUsers(c *gin.Context) {
	ctx := c.Request.Context()
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	cursor, _ := strconv.ParseUint(c.DefaultQuery("cursor", "0"), 10, 64)
	q := c.Query("q")

	query := s.db.WithContext(ctx).Model(&persistence.User{})
	if cursor > 0 {
		query = query.Where("id < ?", cursor)
	}
	if q != "" {
		like := "%" + q + "%"
		query = query.Where("email LIKE ? OR phone LIKE ?", like, like)
	}
	var users []persistence.User
	if err := query.Order("id DESC").Limit(limit).Find(&users).Error; err != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "list users"))
		return
	}
	if len(users) == 0 {
		c.JSON(http.StatusOK, gin.H{"users": []adminUserRow{}})
		return
	}

	userIDs := make([]uint64, len(users))
	for i, u := range users {
		userIDs[i] = u.ID
	}

	var accounts []persistence.CreditAccount
	s.db.WithContext(ctx).Where("user_id IN ?", userIDs).Find(&accounts)
	acctByUser := map[uint64]persistence.CreditAccount{}
	for _, a := range accounts {
		acctByUser[a.UserID] = a
	}

	var spentRows []struct {
		UserID uint64
		N      int64
	}
	s.db.WithContext(ctx).Model(&persistence.CreditLedger{}).
		Where("user_id IN ? AND direction = ?", userIDs, "commit").
		Select("user_id, COALESCE(SUM(amount), 0) as n").Group("user_id").Scan(&spentRows)
	spentByUser := map[uint64]int64{}
	for _, r := range spentRows {
		spentByUser[r.UserID] = -r.N // commit amounts are negative, see handleAdminOverview's own doc
	}

	var jobCountRows []struct {
		UserID uint64
		N      int64
	}
	s.db.WithContext(ctx).Model(&persistence.Job{}).
		Where("user_id IN ?", userIDs).
		Select("user_id, COUNT(*) as n").Group("user_id").Scan(&jobCountRows)
	jobCountByUser := map[uint64]int64{}
	for _, r := range jobCountRows {
		jobCountByUser[r.UserID] = r.N
	}

	out := make([]adminUserRow, 0, len(users))
	for _, u := range users {
		acct := acctByUser[u.ID]
		out = append(out, adminUserRow{
			BizID:        u.BizID,
			Email:        u.Email,
			Phone:        u.Phone,
			IsAdmin:      u.IsAdmin,
			Balance:      acct.Balance,
			Held:         acct.Held,
			CreditsSpent: int(spentByUser[u.ID]),
			JobCount:     int(jobCountByUser[u.ID]),
			CreatedAt:    u.CreatedAt,
		})
	}

	resp := gin.H{"users": out}
	if len(users) == limit {
		resp["next_cursor"] = strconv.FormatUint(users[len(users)-1].ID, 10)
	}
	c.JSON(http.StatusOK, resp)
}

type grantCreditsRequest struct {
	Amount int    `json:"amount" binding:"required"`
	Remark string `json:"remark"`
}

// maxAdminGrant is a sanity ceiling on one manual grant — this is an
// operator-trusted action (same trust level as cmd/cli's grant-credits),
// not a place expecting abuse, but a fat-fingered extra zero shouldn't be
// able to hand out an absurd balance with no backstop at all.
const maxAdminGrant = 1_000_000

// handleAdminGrantCredits is POST /api/v1/admin/users/{bizID}/credits: the
// web-UI equivalent of cmd/cli's `grant-credits`, going through the exact
// same creditsvc.Recharge (recharge_custom, admin-supplied remark) so both
// paths render identically in the recipient's own ledger
// (credits.remark.recharge_custom in zh.json/en.json already expects this
// shape — it predates this handler, written for the CLI tool).
func (s *Server) handleAdminGrantCredits(c *gin.Context) {
	ctx := c.Request.Context()
	bizID := c.Param("bizID")

	var target persistence.User
	if err := s.db.WithContext(ctx).First(&target, "biz_id = ?", bizID).Error; err != nil {
		c.JSON(http.StatusNotFound, errBody("not_found", "user not found"))
		return
	}

	var req grantCreditsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errBody("bad_request", err.Error()))
		return
	}
	if req.Amount <= 0 || req.Amount > maxAdminGrant {
		c.JSON(http.StatusBadRequest, errBody("bad_request", "amount must be between 1 and "+strconv.Itoa(maxAdminGrant)))
		return
	}
	remark := req.Remark
	if remark == "" {
		remark = "admin grant"
	}

	idemKey := "admin_grant:" + bizID + ":" + id.New()
	if err := s.credits.Recharge(ctx, target.ID, idemKey, req.Amount, remark); err != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "grant failed"))
		return
	}

	var acct persistence.CreditAccount
	_ = s.db.WithContext(ctx).First(&acct, "user_id = ?", target.ID).Error
	c.JSON(http.StatusOK, gin.H{"balance": acct.Balance, "held": acct.Held, "credited": req.Amount})
}

type setAdminRequest struct {
	IsAdmin *bool `json:"is_admin" binding:"required"`
}

// handleAdminSetAdmin is POST /api/v1/admin/users/{bizID}/admin: promotes
// or demotes another user. Refuses to demote the last remaining admin —
// with zero admins left, nobody (short of direct DB access, the same
// operator-only path this feature exists to replace) could ever grant
// admin back, an unrecoverable lockout this one check is cheap to prevent.
func (s *Server) handleAdminSetAdmin(c *gin.Context) {
	ctx := c.Request.Context()
	bizID := c.Param("bizID")

	var req setAdminRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errBody("bad_request", err.Error()))
		return
	}

	var target persistence.User
	if err := s.db.WithContext(ctx).First(&target, "biz_id = ?", bizID).Error; err != nil {
		c.JSON(http.StatusNotFound, errBody("not_found", "user not found"))
		return
	}

	if target.IsAdmin && !*req.IsAdmin {
		var adminCount int64
		s.db.WithContext(ctx).Model(&persistence.User{}).Where("is_admin = ?", true).Count(&adminCount)
		if adminCount <= 1 {
			c.JSON(http.StatusConflict, errBody("last_admin", "cannot demote the only remaining admin"))
			return
		}
	}

	if err := s.db.WithContext(ctx).Model(&persistence.User{}).Where("id = ?", target.ID).
		Update("is_admin", *req.IsAdmin).Error; err != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "update admin flag"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"biz_id": target.BizID, "is_admin": *req.IsAdmin})
}

// handleAdminUsage is GET /api/v1/admin/usage?days=30: per-day job counts
// and credits consumed for the trailing N days (default/cap 90) — the
// dashboard's usage-over-time view. Two separate GROUP BY queries (jobs by
// created_at, ledger by created_at) rather than one join, since a day with
// jobs but no commits yet (still running) or commits but the job spans
// midnight would otherwise double-count or drop rows under a naive join.
func (s *Server) handleAdminUsage(c *gin.Context) {
	ctx := c.Request.Context()
	days, _ := strconv.Atoi(c.DefaultQuery("days", "30"))
	if days <= 0 || days > 90 {
		days = 30
	}
	since := time.Now().AddDate(0, 0, -days)

	var jobRows []struct {
		Day string
		N   int64
	}
	s.db.WithContext(ctx).Model(&persistence.Job{}).
		Where("created_at >= ?", since).
		Select("DATE(created_at) as day, COUNT(*) as n").
		Group("DATE(created_at)").Order("day").Scan(&jobRows)

	var creditRows []struct {
		Day string
		N   int64
	}
	s.db.WithContext(ctx).Model(&persistence.CreditLedger{}).
		Where("created_at >= ? AND direction = ?", since, "commit").
		Select("DATE(created_at) as day, COALESCE(SUM(amount), 0) as n").
		Group("DATE(created_at)").Order("day").Scan(&creditRows)

	jobsByDay := map[string]int64{}
	for _, r := range jobRows {
		jobsByDay[r.Day] = r.N
	}
	creditsByDay := map[string]int64{}
	for _, r := range creditRows {
		creditsByDay[r.Day] = -r.N // commit amounts are negative
	}

	daysOut := make([]gin.H, 0, len(jobRows)+len(creditRows))
	seen := map[string]bool{}
	for day := range jobsByDay {
		seen[day] = true
	}
	for day := range creditsByDay {
		seen[day] = true
	}
	daysList := make([]string, 0, len(seen))
	for day := range seen {
		daysList = append(daysList, day)
	}
	sort.Strings(daysList)
	for _, day := range daysList {
		daysOut = append(daysOut, gin.H{
			"day":              day,
			"jobs":             jobsByDay[day],
			"credits_consumed": creditsByDay[day],
		})
	}

	c.JSON(http.StatusOK, gin.H{"days": daysOut})
}
