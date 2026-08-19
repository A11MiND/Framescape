// Package creditsvc implements F1.3/F1.5's credit hold/commit/refund
// lifecycle (PRD §12.3): jobsvc holds an estimate at submission, the
// projection layer commits each node's actual cost as it succeeds, and
// refunds the unused remainder once the workflow reaches a terminal phase.
//
// §12.3's own pseudocode has an arithmetic bug: it has `hold` insert a
// credit_ledger row of amount=-est while ALSO moving that same est from
// balance to held — a pure reallocation with zero net effect on
// balance+held, yet the ledger row makes SUM(ledger.amount) drop by est,
// breaking the reconciliation invariant it defines two paragraphs later
// (`balance+held == SUM(ledger.amount)`) the very first time anyone holds
// credits. Traced through by hand before writing any code here — the
// invariant only actually holds if hold/refund (pure balance<->held moves,
// net-neutral) record amount=0 and only recharge/commit (real value
// entering/leaving escrow) carry a nonzero signed amount. That's what this
// package does; the true hold/refund magnitude is still recorded, just in
// `remark` for audit purposes rather than `amount`.
package creditsvc

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
)

// remarkPayload is what actually lands in credit_ledger.remark: a
// machine-readable kind plus whatever numeric/text context that kind needs,
// not a pre-rendered English sentence — the frontend owns turning `kind`
// into localized text (credits.remark.<kind> in zh.json/en.json), the same
// split already used for job cost breakdowns (EstimateItem.Kind). Rows
// written before this existed still hold a plain English sentence in this
// column; handleCreditsLedger falls back to showing that verbatim when it
// fails to parse as JSON, so old history doesn't break, it just stays
// untranslated.
type remarkPayload struct {
	Kind     string  `json:"kind"`
	Amount   int     `json:"amount,omitempty"`
	Workflow string  `json:"workflow,omitempty"`
	CostYuan float64 `json:"cost_yuan,omitempty"`
	Text     string  `json:"text,omitempty"` // recharge_custom only: the operator's own CLI-supplied text, inherently unlocalizable
}

func encodeRemark(p remarkPayload) string {
	b, err := json.Marshal(p)
	if err != nil {
		return p.Kind
	}
	return string(b)
}

// marginK is §12.2's configurable margin factor (POC default 2.0).
const marginK = 2.0

// videoRateYuan is §12.1's per-second price by resolution.
var videoRateYuan = map[string]float64{"768P": 0.50, "2K": 0.80}

const imageRateYuan = 0.025

// CreditsFromYuan implements §12.2: ceil(cost_yuan * K / 0.10), minimum 1
// (only for a strictly positive cost — zero cost is zero credits, e.g. a
// completely failed generation with no successful output).
func CreditsFromYuan(costYuan float64) int {
	if costYuan <= 0 {
		return 0
	}
	c := int(math.Ceil(costYuan * marginK / 0.10))
	if c < 1 {
		c = 1
	}
	return c
}

// ErrInsufficientBalance is returned by Hold when the user doesn't have
// enough available balance — jobsvc.Create surfaces this as a 4xx, never
// silently degrades.
var ErrInsufficientBalance = errors.New("insufficient credit balance")

type Service struct {
	db *sql.DB
}

func New(db *sql.DB) *Service {
	return &Service{db: db}
}

// Hold implements §12.3's submit-time step: CAS-decrement balance / CAS-
// increment held, then an idempotent ledger row. idemKey must be unique per
// hold operation (jobsvc uses "job:{bizID}:hold" and, for video.sequence's
// upgrade step, "job:{bizID}:hold:upgrade" — §12.3's own two-phase design).
// A retry with the same idemKey is a safe no-op (unique index on idem_key).
// kind is one of "job"/"retry"/"draft"/"upgrade" (why this hold happened,
// becomes remark's "hold_"+kind); workflow is the job's workflow name, for
// kinds where that varies (job/retry) — pass "" where it doesn't (draft/
// upgrade are always video.sequence, baked into their own translated string).
func (s *Service) Hold(ctx context.Context, userID uint64, idemKey, refType, refID string, amount int, kind, workflow string) error {
	if amount <= 0 {
		return nil
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if exists, err := idemKeyExists(ctx, tx, idemKey); err != nil || exists {
			return err
		}
		res, err := tx.ExecContext(ctx, `
			UPDATE credit_accounts SET balance = balance - ?, held = held + ?, version = version + 1
			WHERE user_id = ? AND balance >= ?`, amount, amount, userID, amount)
		if err != nil {
			return fmt.Errorf("hold: update credit_accounts: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrInsufficientBalance
		}
		balance, held, err := readAccount(ctx, tx, userID)
		if err != nil {
			return err
		}
		return insertLedger(ctx, tx, ledgerRow{
			UserID: userID, Direction: "hold", Amount: 0, BalanceAfter: balance, HeldAfter: held,
			RefType: refType, RefID: refID, IdemKey: idemKey,
			Remark: encodeRemark(remarkPayload{Kind: "hold_" + kind, Amount: amount, Workflow: workflow}),
		})
	})
}

// Commit implements §12.3's per-node settlement: the executor's actual
// cost_yuan output converts to credits, held shrinks by that amount (never
// balance — the money was already moved out of balance at Hold time), and
// this is the one operation whose ledger row carries the real negative
// delta (this credit permanently leaves escrow, spent on a real provider
// call). idemKey is "task_run:{taskRunID}:commit" — each TaskRunID commits
// at most once no matter how many times its completion event is delivered.
// Commit returns the amount actually deducted from held, which callers must
// use for any downstream bookkeeping (jobs.credit_settled) instead of
// recomputing CreditsFromYuan(costYuan) themselves — see the comment below
// for why those two numbers can legitimately differ.
func (s *Service) Commit(ctx context.Context, userID uint64, idemKey, taskRunID string, costYuan float64) (int, error) {
	amount := CreditsFromYuan(costYuan)
	if amount <= 0 {
		return 0, nil
	}
	var actual int
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		if exists, err := idemKeyExists(ctx, tx, idemKey); err != nil || exists {
			return err
		}
		// Read held under this transaction's row lock — the actual deduction
		// has to be computed from a value that can't change between this read
		// and the UPDATE below.
		var heldBefore int
		if err := tx.QueryRowContext(ctx, `SELECT held FROM credit_accounts WHERE user_id = ? FOR UPDATE`, userID).Scan(&heldBefore); err != nil {
			return fmt.Errorf("commit: read held: %w", err)
		}
		// held can't go negative. A node's actual cost is *usually* covered
		// by what was held for the whole job (the estimate is deliberately
		// generous), but it can legitimately be exceeded: image.comic4/
		// image.sequence bill each Loop node independently, so §12.2's
		// "minimum 1 credit" floor applies per node, not once across the
		// batch estimate — 4 panels at ¥0.025 each floor to 4 credits total,
		// not the 2 a single combined-cost estimate assumes. Same shape for
		// any token-priced node (H3-Context-IR, MiniMax-M3) whose real usage
		// runs past its estimate. When held is insufficient, only what's
		// actually there leaves the system — the shortfall is an unbilled
		// loss the platform absorbs, not phantom credits vanishing from the
		// ledger. Logging the pre-clamp nominal `amount` here unconditionally
		// (instead of this actual, clamped deduction) is exactly what broke
		// balance+held==SUM(credit_ledger.amount) in production data — held
		// legitimately floored at 0 while the ledger kept logging -1 per
		// commit past that point, traced by hand from real accumulated data
		// before this fix (credit_ledger.held_after showing repeated 0s while
		// amount kept decrementing).
		actual = amount
		if heldBefore < actual {
			actual = heldBefore
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE credit_accounts SET held = held - ?, version = version + 1
			WHERE user_id = ?`, actual, userID); err != nil {
			return fmt.Errorf("commit: update credit_accounts: %w", err)
		}
		balance, held, err := readAccount(ctx, tx, userID)
		if err != nil {
			return err
		}
		return insertLedger(ctx, tx, ledgerRow{
			UserID: userID, Direction: "commit", Amount: -actual, BalanceAfter: balance, HeldAfter: held,
			RefType: "task_run", RefID: taskRunID, IdemKey: idemKey,
			Remark: encodeRemark(remarkPayload{Kind: "commit", Amount: actual, CostYuan: costYuan}),
		})
	})
	return actual, err
}

// Refund implements §12.3's terminal-state settlement: whatever's left in
// held for this job (after all its nodes' commits) goes back to balance.
// credit_accounts pools balance/held per user, not per job, so the caller
// computes the release amount itself from jobs.credit_held minus
// jobs.credit_settled (both running counters jobsvc/the projection layer
// keep updated — see jobsvc.go's Hold call sites and projection.go's
// commit/refund hooks) and passes it in directly.
func (s *Service) Refund(ctx context.Context, userID uint64, idemKey, jobBizID string, amount int) error {
	if amount <= 0 {
		return nil
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if exists, err := idemKeyExists(ctx, tx, idemKey); err != nil || exists {
			return err
		}
		// Same reasoning as Commit's fix: the caller's `amount` (jobs.
		// credit_held - jobs.credit_settled) is only correct if every prior
		// commit against this job billed exactly what it claimed — clamp
		// against what's actually left in held rather than trusting the
		// caller's arithmetic, so a stale/incorrect credit_settled can't mint
		// balance that was never really held (unconditionally crediting the
		// full nominal `amount` back to balance while held could only give up
		// less would inflate balance+held with zero ledger trace, since
		// refund's ledger amount is always 0 by design — the other way this
		// invariant can break).
		var heldBefore int
		if err := tx.QueryRowContext(ctx, `SELECT held FROM credit_accounts WHERE user_id = ? FOR UPDATE`, userID).Scan(&heldBefore); err != nil {
			return fmt.Errorf("refund: read held: %w", err)
		}
		actual := amount
		if heldBefore < actual {
			actual = heldBefore
		}
		if actual <= 0 {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE credit_accounts SET balance = balance + ?, held = held - ?, version = version + 1
			WHERE user_id = ?`, actual, actual, userID); err != nil {
			return fmt.Errorf("refund: update credit_accounts: %w", err)
		}
		balance, held, err := readAccount(ctx, tx, userID)
		if err != nil {
			return err
		}
		return insertLedger(ctx, tx, ledgerRow{
			UserID: userID, Direction: "refund", Amount: 0, BalanceAfter: balance, HeldAfter: held,
			RefType: "job", RefID: jobBizID, IdemKey: idemKey,
			Remark: encodeRemark(remarkPayload{Kind: "refund", Amount: actual}),
		})
	})
}

// Recharge is F1.6's manual grant (cmd/cli) — the only direction with no
// counterpart consuming it later, a straightforward addition to balance.
// remark is an operator-supplied CLI flag, free text by nature (there's no
// fixed set of reasons to translate), so it's stored as recharge_custom's
// raw `text` rather than a `kind` the frontend can localize.
func (s *Service) Recharge(ctx context.Context, userID uint64, idemKey string, amount int, remark string) error {
	return s.recharge(ctx, userID, idemKey, amount, encodeRemark(remarkPayload{Kind: "recharge_custom", Amount: amount, Text: remark}))
}

// RechargeDemo is handleCreditsTopup's self-serve demo grant (§07's "充值
// 入口" gap) — same mechanics as Recharge, but a fixed, translatable reason
// instead of an arbitrary operator string.
func (s *Service) RechargeDemo(ctx context.Context, userID uint64, idemKey string, amount int) error {
	return s.recharge(ctx, userID, idemKey, amount, encodeRemark(remarkPayload{Kind: "recharge_demo", Amount: amount}))
}

// GrantStreak is communitysvc.Service.RecordPublish's payout for crossing a
// daily-publish streak milestone (3/10/30 days) — same mechanics as
// RechargeDemo, kept as its own method rather than a shared "bonus" one
// because the ledger's remark.kind varies by milestone
// (community_streak_3/10/30, each its own zh.json/en.json string) so a
// user's credit history reads "连续发布3天奖励" rather than a generic
// "bonus credited". idemKey is communitysvc's own
// "community_streak:{userID}:{days}:{date}", so a retried RecordPublish
// call can never double-grant the same day's milestone.
func (s *Service) GrantStreak(ctx context.Context, userID uint64, idemKey string, streakDays, amount int) error {
	return s.recharge(ctx, userID, idemKey, amount, encodeRemark(remarkPayload{Kind: fmt.Sprintf("community_streak_%d", streakDays), Amount: amount}))
}

func (s *Service) recharge(ctx context.Context, userID uint64, idemKey string, amount int, remark string) error {
	if amount <= 0 {
		return fmt.Errorf("recharge amount must be positive, got %d", amount)
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if exists, err := idemKeyExists(ctx, tx, idemKey); err != nil || exists {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE credit_accounts SET balance = balance + ?, version = version + 1 WHERE user_id = ?`,
			amount, userID); err != nil {
			return fmt.Errorf("recharge: update credit_accounts: %w", err)
		}
		balance, held, err := readAccount(ctx, tx, userID)
		if err != nil {
			return err
		}
		return insertLedger(ctx, tx, ledgerRow{
			UserID: userID, Direction: "recharge", Amount: amount, BalanceAfter: balance, HeldAfter: held,
			RefType: "admin", RefID: "cli", IdemKey: idemKey, Remark: remark,
		})
	})
}

type ledgerRow struct {
	UserID       uint64
	Direction    string
	Amount       int
	BalanceAfter int
	HeldAfter    int
	RefType      string
	RefID        string
	IdemKey      string
	Remark       string
}

func insertLedger(ctx context.Context, tx *sql.Tx, r ledgerRow) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO credit_ledger (user_id, direction, amount, balance_after, held_after, ref_type, ref_id, idem_key, remark)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.UserID, r.Direction, r.Amount, r.BalanceAfter, r.HeldAfter, r.RefType, r.RefID, r.IdemKey, r.Remark)
	if err != nil {
		return fmt.Errorf("insert credit_ledger: %w", err)
	}
	return nil
}

func idemKeyExists(ctx context.Context, tx *sql.Tx, idemKey string) (bool, error) {
	var one int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM credit_ledger WHERE idem_key = ? LIMIT 1`, idemKey).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check idem_key: %w", err)
	}
	return true, nil
}

func readAccount(ctx context.Context, tx *sql.Tx, userID uint64) (balance, held int, err error) {
	err = tx.QueryRowContext(ctx, `SELECT balance, held FROM credit_accounts WHERE user_id = ?`, userID).Scan(&balance, &held)
	if err != nil {
		return 0, 0, fmt.Errorf("read credit_accounts: %w", err)
	}
	return balance, held, nil
}

func (s *Service) withTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// EstimateImageCredits/EstimateVideoCredits let jobsvc compute a Hold amount
// without importing this package's internal pricing tables directly.
func EstimateImageCredits(n int) int {
	if n <= 0 {
		n = 1
	}
	return CreditsFromYuan(float64(n) * imageRateYuan)
}

// EstimatePerNodeImageCredits is for workflows where each image is its own
// independently-billed node — image.comic4/image.sequence's Loop issues one
// minimax.image call per panel/shot, unlike image.single's single call for
// n images (which also covers what used to be the separate image.batch
// workflow_name). Each node pays §12.2's "minimum 1 credit" floor on its own, so
// summing n independent floors is the correct hold estimate; using
// EstimateImageCredits(n)'s single-combined-cost formula instead
// undercounts whenever n separate floors exceed one batch floor (4 panels
// at ¥0.025 each floor to 4 credits total, not the 2 a combined-cost
// estimate assumes) — this exact mismatch is what broke
// balance+held==SUM(credit_ledger.amount) in production data (see
// Commit's doc for the other half of that fix).
func EstimatePerNodeImageCredits(n int) int {
	if n <= 0 {
		n = 1
	}
	return n * CreditsFromYuan(imageRateYuan)
}

func EstimateVideoCredits(durationSeconds int, resolution string) int {
	rate, ok := videoRateYuan[resolution]
	if !ok {
		rate = videoRateYuan["768P"]
	}
	return CreditsFromYuan(float64(durationSeconds) * rate)
}

// EstimatePromptEnhanceCredits is F6.10's hold-time safety margin for the
// optional H3-Context-IR node: real cost is settled per §3.4's token-based
// pricing from the node's own cost-yuan output (same commit/refund path as
// every other node), so this only needs to be a conservative upper bound, not
// exact. ~500 prompt + ~1500 completion tokens is a generous estimate for a
// single video prompt's worth of structured-description output.
func EstimatePromptEnhanceCredits() int {
	const estInputTokens, estOutputTokens = 500.0, 1500.0
	return CreditsFromYuan(estInputTokens/1_000_000*5.80 + estOutputTokens/1_000_000*23.00)
}

// EstimateStorySplitCredits is F5.4's hold-time safety margin for
// minimax.text.split_story — same "conservative upper bound, real cost
// settles from the node's own cost-yuan output" reasoning as
// EstimatePromptEnhanceCredits.
func EstimateStorySplitCredits() int {
	const estInputTokens, estOutputTokens = 300.0, 400.0
	return CreditsFromYuan(estInputTokens/1_000_000*2.10 + estOutputTokens/1_000_000*8.40)
}
