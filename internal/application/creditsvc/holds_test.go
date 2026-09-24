package creditsvc

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/go-sql-driver/mysql"

	"aigc-platform/internal/pkg/config"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("mysql", config.MySQLDSN())
	if err != nil {
		t.Fatalf("open mysql: %v", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		t.Skipf("no local MySQL available, skipping: %v", err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = 'credit_holds'`).Scan(&n); err != nil || n == 0 {
		db.Close()
		t.Skip("credit_holds not migrated")
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func seedAccount(t *testing.T, db *sql.DB, svc *Service, userID uint64, balance int) {
	t.Helper()
	cleanup := func() {
		_, _ = db.Exec(`DELETE FROM credit_accounts WHERE user_id = ?`, userID)
		_, _ = db.Exec(`DELETE FROM credit_ledger WHERE user_id = ?`, userID)
		_, _ = db.Exec(`DELETE FROM credit_holds WHERE user_id = ?`, userID)
	}
	cleanup()
	t.Cleanup(cleanup)
	if _, err := db.Exec(`INSERT INTO credit_accounts (user_id, balance, held) VALUES (?, 0, 0)`, userID); err != nil {
		t.Fatal(err)
	}
	if balance > 0 {
		if err := svc.Recharge(context.Background(), userID, "test:seed", balance, "seed"); err != nil {
			t.Fatal(err)
		}
	}
}

func inTx(t *testing.T, db *sql.DB, fn func(tx *sql.Tx) error) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func account(t *testing.T, db *sql.DB, userID uint64) (balance, held, ledger int) {
	t.Helper()
	if err := db.QueryRow(`SELECT balance, held FROM credit_accounts WHERE user_id = ?`, userID).Scan(&balance, &held); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COALESCE(SUM(amount), 0) FROM credit_ledger WHERE user_id = ?`, userID).Scan(&ledger); err != nil {
		t.Fatal(err)
	}
	return
}

func TestPerJobHoldsDoNotLeakAcrossJobs(t *testing.T) {
	db := openTestDB(t)
	svc := New(db)
	ctx := context.Background()
	const userID = 999999101
	seedAccount(t, db, svc, userID, 100)
	a := JobHold{UserID: userID, JobID: 999999101001, JobBizID: "test-hold-job-a", Workflow: "t"}
	b := JobHold{UserID: userID, JobID: 999999101002, JobBizID: "test-hold-job-b", Workflow: "t"}

	inTx(t, db, func(tx *sql.Tx) error { return svc.HoldForJobTx(ctx, tx, a, "test:a:hold", 10, "job") })
	inTx(t, db, func(tx *sql.Tx) error { return svc.HoldForJobTx(ctx, tx, b, "test:b:hold", 5, "job") })
	inTx(t, db, func(tx *sql.Tx) error { return svc.HoldForJobTx(ctx, tx, a, "test:a:hold", 10, "job") }) // idempotent

	// Job A overspends its reservation: 12 credits against 10 held.
	inTx(t, db, func(tx *sql.Tx) error {
		charged, err := svc.CommitForJobTx(ctx, tx, a, "test:a:c1", "gen", 0.6)
		if err == nil && charged != 12 {
			t.Errorf("charged %d, want 12", charged)
		}
		return err
	})
	inTx(t, db, func(tx *sql.Tx) error {
		charged, err := svc.CommitForJobTx(ctx, tx, a, "test:a:c1", "gen", 0.6)
		if err == nil && charged != 0 {
			t.Errorf("repeated commit charged %d", charged)
		}
		return err
	})
	balance, held, ledger := account(t, db, userID)
	if held != 5 {
		t.Fatalf("held = %d, want 5 (job B untouched)", held)
	}
	if balance != 83 {
		t.Fatalf("balance = %d, want 83 (100 - 15 held - 2 overage)", balance)
	}
	if balance+held != ledger {
		t.Fatalf("invariant broken: %d + %d != %d", balance, held, ledger)
	}

	inTx(t, db, func(tx *sql.Tx) error { _, err := svc.ReleaseJobTx(ctx, tx, a, "test:a:release"); return err })
	inTx(t, db, func(tx *sql.Tx) error {
		_, err := svc.CommitForJobTx(ctx, tx, b, "test:b:c1", "gen", 0.025)
		return err
	})
	inTx(t, db, func(tx *sql.Tx) error {
		released, err := svc.ReleaseJobTx(ctx, tx, b, "test:b:release")
		if err == nil && released != 4 {
			t.Errorf("released %d, want 4", released)
		}
		return err
	})
	balance, held, ledger = account(t, db, userID)
	if held != 0 || balance != 87 || balance+held != ledger {
		t.Fatalf("final balance=%d held=%d ledger=%d, want 87/0/87 (100 - 12 - 1)", balance, held, ledger)
	}
	mismatches, err := svc.AuditHolds(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range mismatches {
		if m.UserID == userID {
			t.Fatalf("audit mismatch: %+v", m)
		}
	}
}

func TestCommitNeverDrivesBalanceNegative(t *testing.T) {
	db := openTestDB(t)
	svc := New(db)
	ctx := context.Background()
	const userID = 999999102
	seedAccount(t, db, svc, userID, 1)
	job := JobHold{UserID: userID, JobID: 999999102001, JobBizID: "test-hold-job-c", Workflow: "t"}
	inTx(t, db, func(tx *sql.Tx) error { return svc.HoldForJobTx(ctx, tx, job, "test:c:hold", 1, "job") })
	inTx(t, db, func(tx *sql.Tx) error {
		charged, err := svc.CommitForJobTx(ctx, tx, job, "test:c:c1", "gen", 0.25)
		if err == nil && charged != 1 {
			t.Errorf("charged %d, want 1 (only the reservation was available)", charged)
		}
		return err
	})
	balance, held, ledger := account(t, db, userID)
	if balance != 0 || held != 0 || ledger != 0 {
		t.Fatalf("balance=%d held=%d ledger=%d, want 0/0/0", balance, held, ledger)
	}
}

func TestHoldFailsOnInsufficientBalance(t *testing.T) {
	db := openTestDB(t)
	svc := New(db)
	const userID = 999999103
	seedAccount(t, db, svc, userID, 3)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	err = svc.HoldForJobTx(context.Background(), tx, JobHold{UserID: userID, JobID: 999999103001, JobBizID: "test-hold-job-d"}, "test:d:hold", 5, "job")
	if err != ErrInsufficientBalance {
		t.Fatalf("err = %v, want ErrInsufficientBalance", err)
	}
}
