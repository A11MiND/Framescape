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
	"errors"
	"fmt"
	"math"
)

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
func (s *Service) Hold(ctx context.Context, userID uint64, idemKey, refType, refID string, amount int, remark string) error {
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
			Remark: fmt.Sprintf("held %d credits: %s", amount, remark),
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
func (s *Service) Commit(ctx context.Context, userID uint64, idemKey, taskRunID string, costYuan float64) error {
	amount := CreditsFromYuan(costYuan)
	if amount <= 0 {
		return nil
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if exists, err := idemKeyExists(ctx, tx, idemKey); err != nil || exists {
			return err
		}
		// held can't go negative — a node's actual cost should never exceed
		// what was held for the whole job (the estimate is deliberately
		// generous), but clamp defensively via GREATEST rather than trust it.
		if _, err := tx.ExecContext(ctx, `
			UPDATE credit_accounts SET held = GREATEST(held - ?, 0), version = version + 1
			WHERE user_id = ?`, amount, userID); err != nil {
			return fmt.Errorf("commit: update credit_accounts: %w", err)
		}
		balance, held, err := readAccount(ctx, tx, userID)
		if err != nil {
			return err
		}
		return insertLedger(ctx, tx, ledgerRow{
			UserID: userID, Direction: "commit", Amount: -amount, BalanceAfter: balance, HeldAfter: held,
			RefType: "task_run", RefID: taskRunID, IdemKey: idemKey,
			Remark: fmt.Sprintf("committed %d credits (cost %.4f yuan)", amount, costYuan),
		})
	})
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
		if _, err := tx.ExecContext(ctx, `
			UPDATE credit_accounts SET balance = balance + ?, held = GREATEST(held - ?, 0), version = version + 1
			WHERE user_id = ?`, amount, amount, userID); err != nil {
			return fmt.Errorf("refund: update credit_accounts: %w", err)
		}
		balance, held, err := readAccount(ctx, tx, userID)
		if err != nil {
			return err
		}
		return insertLedger(ctx, tx, ledgerRow{
			UserID: userID, Direction: "refund", Amount: 0, BalanceAfter: balance, HeldAfter: held,
			RefType: "job", RefID: jobBizID, IdemKey: idemKey,
			Remark: fmt.Sprintf("refunded %d unused held credits", amount),
		})
	})
}

// Recharge is F1.6's manual grant (cmd/creditcli) — the only direction with
// no counterpart consuming it later, a straightforward addition to balance.
func (s *Service) Recharge(ctx context.Context, userID uint64, idemKey string, amount int, remark string) error {
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
