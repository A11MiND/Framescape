package creditsvc

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Per-job reservations (credit_holds). credit_accounts.held equals the sum
// of open holds' remaining, so settling one job never touches another job's
// reservation. All methods run inside the caller's transaction and take row
// locks in the order credit_holds, credit_accounts; callers that also lock
// jobs rows must lock those first.

// JobHold identifies the job a reservation belongs to.
type JobHold struct {
	UserID   uint64
	JobID    uint64
	JobBizID string
	Workflow string
}

// HoldForJobTx moves amount from balance into the job's reservation.
// Repeating idemKey is a no-op.
func (s *Service) HoldForJobTx(ctx context.Context, tx *sql.Tx, h JobHold, idemKey string, amount int, kind string) error {
	if amount <= 0 {
		return nil
	}
	if exists, err := idemKeyExists(ctx, tx, idemKey); err != nil || exists {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO credit_holds (user_id, job_id, job_biz_id) VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE job_id = job_id`, h.UserID, h.JobID, h.JobBizID); err != nil {
		return fmt.Errorf("hold: ensure credit_holds row: %w", err)
	}
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM credit_holds WHERE job_id = ? FOR UPDATE`, h.JobID).Scan(&status); err != nil {
		return fmt.Errorf("hold: lock credit_holds: %w", err)
	}
	if status != "open" {
		return fmt.Errorf("hold: reservation of job %s is already closed", h.JobBizID)
	}
	res, err := tx.ExecContext(ctx, `UPDATE credit_accounts SET balance = balance - ?, held = held + ?, version = version + 1
		WHERE user_id = ? AND balance >= ?`, amount, amount, h.UserID, amount)
	if err != nil {
		return fmt.Errorf("hold: update credit_accounts: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrInsufficientBalance
	}
	if _, err := tx.ExecContext(ctx, `UPDATE credit_holds SET held_total = held_total + ?, remaining = remaining + ? WHERE job_id = ?`,
		amount, amount, h.JobID); err != nil {
		return fmt.Errorf("hold: update credit_holds: %w", err)
	}
	balance, held, err := readAccount(ctx, tx, h.UserID)
	if err != nil {
		return err
	}
	return insertLedger(ctx, tx, ledgerRow{
		UserID: h.UserID, Direction: "hold", Amount: 0, BalanceAfter: balance, HeldAfter: held,
		RefType: "job", RefID: h.JobBizID, IdemKey: idemKey,
		Remark: encodeRemark(remarkPayload{Kind: "hold_" + kind, Amount: amount, Workflow: h.Workflow}),
	})
}

// CommitForJobTx charges the credits for costYuan. The job's reservation is
// used first; any excess comes from the spendable balance as far as it
// goes, since a reservation is an estimate rather than a price ceiling.
// A shortfall beyond the balance is absorbed and recorded in the remark.
func (s *Service) CommitForJobTx(ctx context.Context, tx *sql.Tx, h JobHold, idemKey, node string, costYuan float64) (int, error) {
	amount := CreditsFromYuan(costYuan)
	if amount <= 0 {
		return 0, nil
	}
	if exists, err := idemKeyExists(ctx, tx, idemKey); err != nil || exists {
		return 0, err
	}
	remaining := 0
	err := tx.QueryRowContext(ctx, `SELECT remaining FROM credit_holds WHERE job_id = ? FOR UPDATE`, h.JobID).Scan(&remaining)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("commit: lock credit_holds: %w", err)
	}
	holdExists := err == nil
	var balance, held int
	if err := tx.QueryRowContext(ctx, `SELECT balance, held FROM credit_accounts WHERE user_id = ? FOR UPDATE`, h.UserID).Scan(&balance, &held); err != nil {
		return 0, fmt.Errorf("commit: lock credit_accounts: %w", err)
	}
	fromHold := min(amount, remaining, held)
	fromBalance := min(amount-fromHold, max(balance, 0))
	charged := fromHold + fromBalance
	if charged == 0 {
		return 0, insertLedger(ctx, tx, ledgerRow{
			UserID: h.UserID, Direction: "commit", Amount: 0, BalanceAfter: balance, HeldAfter: held,
			RefType: "job", RefID: h.JobBizID, IdemKey: idemKey,
			Remark: encodeRemark(remarkPayload{Kind: "commit", CostYuan: costYuan, Node: node, Shortfall: amount}),
		})
	}
	if _, err := tx.ExecContext(ctx, `UPDATE credit_accounts SET held = held - ?, balance = balance - ?, version = version + 1 WHERE user_id = ?`,
		fromHold, fromBalance, h.UserID); err != nil {
		return 0, fmt.Errorf("commit: update credit_accounts: %w", err)
	}
	if holdExists {
		if _, err := tx.ExecContext(ctx, `UPDATE credit_holds SET remaining = remaining - ?, committed = committed + ?, overage = overage + ? WHERE job_id = ?`,
			fromHold, fromHold, fromBalance, h.JobID); err != nil {
			return 0, fmt.Errorf("commit: update credit_holds: %w", err)
		}
	}
	balance, held, err = readAccount(ctx, tx, h.UserID)
	if err != nil {
		return 0, err
	}
	return charged, insertLedger(ctx, tx, ledgerRow{
		UserID: h.UserID, Direction: "commit", Amount: -charged, BalanceAfter: balance, HeldAfter: held,
		RefType: "job", RefID: h.JobBizID, IdemKey: idemKey,
		Remark: encodeRemark(remarkPayload{Kind: "commit", Amount: charged, CostYuan: costYuan, Node: node, Shortfall: amount - charged}),
	})
}

// ReleaseJobTx returns the unused reservation to the balance and closes it.
func (s *Service) ReleaseJobTx(ctx context.Context, tx *sql.Tx, h JobHold, idemKey string) (int, error) {
	if exists, err := idemKeyExists(ctx, tx, idemKey); err != nil || exists {
		return 0, err
	}
	var remaining int
	var status string
	err := tx.QueryRowContext(ctx, `SELECT remaining, status FROM credit_holds WHERE job_id = ? FOR UPDATE`, h.JobID).Scan(&remaining, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("release: lock credit_holds: %w", err)
	}
	if status != "open" {
		return 0, nil
	}
	var held int
	if err := tx.QueryRowContext(ctx, `SELECT held FROM credit_accounts WHERE user_id = ? FOR UPDATE`, h.UserID).Scan(&held); err != nil {
		return 0, fmt.Errorf("release: lock credit_accounts: %w", err)
	}
	amount := min(remaining, held)
	if _, err := tx.ExecContext(ctx, `UPDATE credit_holds SET remaining = 0, released = released + ?, status = 'closed' WHERE job_id = ?`, amount, h.JobID); err != nil {
		return 0, fmt.Errorf("release: close credit_holds: %w", err)
	}
	if amount <= 0 {
		return 0, nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE credit_accounts SET balance = balance + ?, held = held - ?, version = version + 1 WHERE user_id = ?`,
		amount, amount, h.UserID); err != nil {
		return 0, fmt.Errorf("release: update credit_accounts: %w", err)
	}
	balance, heldAfter, err := readAccount(ctx, tx, h.UserID)
	if err != nil {
		return 0, err
	}
	return amount, insertLedger(ctx, tx, ledgerRow{
		UserID: h.UserID, Direction: "refund", Amount: 0, BalanceAfter: balance, HeldAfter: heldAfter,
		RefType: "job", RefID: h.JobBizID, IdemKey: idemKey,
		Remark: encodeRemark(remarkPayload{Kind: "refund", Amount: amount}),
	})
}

// HoldMismatch is one user whose held credits disagree with their open
// reservations.
type HoldMismatch struct {
	UserID       uint64
	Held         int
	OpenHoldSum  int
	LedgerSum    int
	BalanceTotal int
}

// AuditHolds checks both invariants: balance+held equals the ledger sum,
// and for users whose reservations are all tracked per job, held equals the
// sum of open reservations.
func (s *Service) AuditHolds(ctx context.Context) ([]HoldMismatch, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.user_id, a.held, a.balance + a.held,
		       COALESCE((SELECT SUM(remaining) FROM credit_holds h WHERE h.user_id = a.user_id AND h.status = 'open'), 0),
		       COALESCE((SELECT SUM(amount) FROM credit_ledger l WHERE l.user_id = a.user_id), 0)
		FROM credit_accounts a
		WHERE EXISTS (SELECT 1 FROM credit_holds h WHERE h.user_id = a.user_id)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HoldMismatch
	for rows.Next() {
		var m HoldMismatch
		if err := rows.Scan(&m.UserID, &m.Held, &m.BalanceTotal, &m.OpenHoldSum, &m.LedgerSum); err != nil {
			return nil, err
		}
		if m.Held != m.OpenHoldSum || m.BalanceTotal != m.LedgerSum {
			out = append(out, m)
		}
	}
	return out, rows.Err()
}
