package creditsvc

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	_ "github.com/go-sql-driver/mysql"

	"aigc-platform/internal/pkg/config"
)

// TestCommitClampReconciliation reproduces, against a real MySQL connection,
// the exact scenario that broke balance+held==SUM(credit_ledger.amount) in
// production data: a job holds credits for a batch of independently-billed
// nodes (image.comic4's Loop — one minimax.image call per panel, each
// paying §12.2's "minimum 1 credit" floor on its own), and the sum of those
// nodes' commits exceeds what was held for the job as a whole (3 held vs.
// 5 real 1-credit floors, mirroring a real traced-by-hand case). Before the
// fix, Commit() logged the pre-clamp nominal amount to credit_ledger even
// after held had already floored at 0 via GREATEST(held-amount,0),
// silently breaking the invariant while looking like normal operation.
func TestCommitClampReconciliation(t *testing.T) {
	db, err := sql.Open("mysql", config.MySQLDSN())
	if err != nil {
		t.Fatalf("open mysql: %v", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		t.Skipf("no local MySQL available, skipping: %v", err)
	}

	const userID = 999999001 // reserved test-only user_id, not a real account
	cleanup := func() {
		_, _ = db.Exec(`DELETE FROM credit_accounts WHERE user_id = ?`, userID)
		_, _ = db.Exec(`DELETE FROM credit_ledger WHERE user_id = ?`, userID)
	}
	cleanup()
	defer cleanup()

	// Seed at zero (0+0==0 trivially satisfies the invariant) and bring the
	// balance up through Recharge itself, not a raw SQL balance write — the
	// invariant only holds when every balance change is ledger-backed, and
	// skipping that for test setup would falsely fail this test regardless
	// of whether Commit/Refund are correct.
	if _, err := db.Exec(`INSERT INTO credit_accounts (user_id, balance, held) VALUES (?, 0, 0)`, userID); err != nil {
		t.Fatalf("seed credit_accounts: %v", err)
	}

	svc := New(db)
	ctx := context.Background()

	if err := svc.Recharge(ctx, userID, "test:recharge", 100, "test recharge"); err != nil {
		t.Fatalf("recharge: %v", err)
	}
	if err := svc.Hold(ctx, userID, "test:hold", "job", "test-job", 3, "test hold"); err != nil {
		t.Fatalf("hold: %v", err)
	}

	// 5 independent commits at 1 credit each (¥0.025 -> ceil(0.025*2/0.1)=1),
	// deliberately exceeding the 3 credits held — this is the shape every
	// image.comic4/image.sequence job with more panels/shots than its old
	// combined-cost estimate assumed used to hit.
	for i := 0; i < 5; i++ {
		if _, err := svc.Commit(ctx, userID, fmt.Sprintf("test:commit:%d", i), fmt.Sprintf("test-task-%d", i), 0.025); err != nil {
			t.Fatalf("commit %d: %v", i, err)
		}
	}

	var balance, held int
	if err := db.QueryRow(`SELECT balance, held FROM credit_accounts WHERE user_id = ?`, userID).Scan(&balance, &held); err != nil {
		t.Fatalf("read account: %v", err)
	}
	var ledgerSum int
	if err := db.QueryRow(`SELECT COALESCE(SUM(amount), 0) FROM credit_ledger WHERE user_id = ?`, userID).Scan(&ledgerSum); err != nil {
		t.Fatalf("read ledger sum: %v", err)
	}

	if held != 0 {
		t.Errorf("expected held to floor at 0 after over-committing past it, got %d", held)
	}
	if got, want := balance+held, ledgerSum; got != want {
		t.Errorf("reconciliation invariant broken: balance+held=%d, SUM(credit_ledger.amount)=%d (want equal)", got, want)
	}
}
