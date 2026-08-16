// F1.3's balance/ledger read surface (§13.2 GET /credits/balance,
// GET /credits/ledger). Pure reads straight off s.db, same as assets.go/
// characters.go's list handlers — every credit_ledger row is written by
// creditsvc's own transactional Hold/Commit/Refund/Recharge (see
// internal/application/creditsvc's doc on why balance+held==SUM(ledger.amount)
// is an invariant), this file never writes to either table.
package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"aigc-platform/internal/infra/persistence"
	"aigc-platform/internal/pkg/id"
)

// demoTopupCredits is F1.3's "充值入口" gap: the POC has no real payment
// integration (the only funding path before this was cmd/cli grant-credits,
// operator-only), and building a real payment gateway is out of scope for a
// POC. This is a self-serve stand-in for the same CLI mechanism — no
// payment fields are collected, it just credits the account directly, so
// it never looks like it's handling real money.
const demoTopupCredits = 200

func (s *Server) handleCreditsBalance(c *gin.Context) {
	var acct persistence.CreditAccount
	if err := s.db.WithContext(c.Request.Context()).First(&acct, "user_id = ?", userID(c)).Error; err != nil {
		c.JSON(http.StatusOK, gin.H{"balance": 0, "held": 0})
		return
	}
	c.JSON(http.StatusOK, gin.H{"balance": acct.Balance, "held": acct.Held})
}

// handleCreditsLedger pages newest-first by a strictly-decreasing numeric id
// cursor, same shape as handleListJobs.
func (s *Server) handleCreditsLedger(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	cursor, _ := strconv.ParseUint(c.DefaultQuery("cursor", "0"), 10, 64)

	q := s.db.WithContext(c.Request.Context()).Where("user_id = ?", userID(c))
	if cursor > 0 {
		q = q.Where("id < ?", cursor)
	}
	var rows []persistence.CreditLedger
	if err := q.Order("id DESC").Limit(limit).Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "list ledger"))
		return
	}

	out := make([]gin.H, 0, len(rows))
	for _, r := range rows {
		out = append(out, gin.H{
			"direction":     r.Direction,
			"amount":        r.Amount,
			"balance_after": r.BalanceAfter,
			"held_after":    r.HeldAfter,
			"ref_type":      r.RefType,
			"ref_id":        r.RefID,
			"remark":        parseRemark(r.Remark),
			"created_at":    r.CreatedAt,
		})
	}
	resp := gin.H{"entries": out}
	if len(rows) == limit {
		resp["next_cursor"] = strconv.FormatUint(rows[len(rows)-1].ID, 10)
	}
	c.JSON(http.StatusOK, resp)
}

// parseRemark turns credit_ledger.remark's stored JSON (creditsvc's own
// remarkPayload) back into a structured object the frontend can localize
// via credits.remark.<kind> — a row written before that JSON encoding
// existed just fails to unmarshal into a nonempty Kind, so it falls back to
// showing its plain-English text verbatim rather than breaking.
func parseRemark(raw string) gin.H {
	var p struct {
		Kind     string  `json:"kind"`
		Amount   int     `json:"amount"`
		Workflow string  `json:"workflow"`
		CostYuan float64 `json:"cost_yuan"`
		Text     string  `json:"text"`
	}
	if err := json.Unmarshal([]byte(raw), &p); err != nil || p.Kind == "" {
		return gin.H{"kind": "", "amount": 0, "workflow": "", "cost_yuan": 0, "text": raw}
	}
	return gin.H{"kind": p.Kind, "amount": p.Amount, "workflow": p.Workflow, "cost_yuan": p.CostYuan, "text": p.Text}
}

// handleCreditsTopup is POST /api/v1/credits/topup: see demoTopupCredits'
// doc for why this exists instead of a real payment flow. idemKey is a
// fresh ULID every call, not derived from any client input — each click is
// a deliberate new top-up, not a retry of a previous one, so nothing here
// should ever get deduplicated the way job submission does.
func (s *Server) handleCreditsTopup(c *gin.Context) {
	uid := userID(c)
	if err := s.credits.RechargeDemo(c.Request.Context(), uid, "topup:"+id.New(), demoTopupCredits); err != nil {
		c.JSON(http.StatusInternalServerError, errBody("internal", "topup failed"))
		return
	}
	var acct persistence.CreditAccount
	_ = s.db.WithContext(c.Request.Context()).First(&acct, "user_id = ?", uid).Error
	c.JSON(http.StatusOK, gin.H{"balance": acct.Balance, "held": acct.Held, "credited": demoTopupCredits})
}
