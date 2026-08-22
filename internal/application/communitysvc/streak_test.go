package communitysvc

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"aigc-platform/internal/application/creditsvc"
	"aigc-platform/internal/pkg/config"
)

// newTestService opens the same local MySQL every other package's DB-backed
// tests use (creditsvc_test.go's own convention: skip rather than fail when
// it's unreachable), seeds a zero-balance credit_accounts row for the given
// reserved test user_id (GrantStreak's UPDATE needs an existing row to
// update), and registers cleanup for every table this package touches.
func newTestService(t *testing.T, userID uint64) (*Service, *sql.DB) {
	t.Helper()
	db, err := sql.Open("mysql", config.MySQLDSN())
	if err != nil {
		t.Fatalf("open mysql: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Skipf("no local MySQL available, skipping: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	cleanup := func() {
		db.Exec(`DELETE FROM community_streaks WHERE user_id = ?`, userID)
		db.Exec(`DELETE FROM community_streak_rewards WHERE user_id = ?`, userID)
		db.Exec(`DELETE FROM community_publish_log WHERE user_id = ?`, userID)
		db.Exec(`DELETE FROM credit_accounts WHERE user_id = ?`, userID)
		db.Exec(`DELETE FROM credit_ledger WHERE user_id = ?`, userID)
	}
	cleanup()
	t.Cleanup(cleanup)

	if _, err := db.Exec(`INSERT INTO credit_accounts (user_id, balance, held) VALUES (?, 0, 0)`, userID); err != nil {
		t.Fatalf("seed credit_accounts: %v", err)
	}

	credits := creditsvc.New(db)
	return New(db, credits), db
}

// backdateStreak directly sets community_streaks' row to simulate an
// existing streak as of a given date — RecordPublish/GetStatus's date math
// is driven by time.Now(), so this is the same technique this codebase's
// own DATE-scanning bug was originally caught with: seed real DB state
// rather than trying to inject a fake clock into either function.
func backdateStreak(t *testing.T, db *sql.DB, userID uint64, streak int, date string) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO community_streaks (user_id, current_streak, last_publish_date) VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE current_streak = VALUES(current_streak), last_publish_date = VALUES(last_publish_date)`,
		userID, streak, date); err != nil {
		t.Fatalf("backdate community_streaks: %v", err)
	}
}

func daysAgo(n int) string { return time.Now().UTC().AddDate(0, 0, -n).Format("2006-01-02") }

func TestRecordPublish_FirstPublish(t *testing.T) {
	const userID = 999999101
	svc, db := newTestService(t, userID)

	awarded, err := svc.RecordPublish(context.Background(), userID)
	if err != nil {
		t.Fatalf("RecordPublish: %v", err)
	}
	if len(awarded) != 0 {
		t.Errorf("awarded = %v, want none (day 1 never hits a milestone)", awarded)
	}

	status, err := svc.GetStatus(context.Background(), userID)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if status.CurrentStreak != 1 {
		t.Errorf("CurrentStreak = %d, want 1", status.CurrentStreak)
	}
	if len(status.PublishedDates) != 1 || status.PublishedDates[0] != daysAgo(0) {
		t.Errorf("PublishedDates = %v, want [%s]", status.PublishedDates, daysAgo(0))
	}
	_ = db
}

func TestRecordPublish_SameDayIsNoOp(t *testing.T) {
	const userID = 999999102
	svc, _ := newTestService(t, userID)
	ctx := context.Background()

	if _, err := svc.RecordPublish(ctx, userID); err != nil {
		t.Fatalf("first call: %v", err)
	}
	awarded, err := svc.RecordPublish(ctx, userID)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if len(awarded) != 0 {
		t.Errorf("second same-day call awarded %v, want none", awarded)
	}
	status, err := svc.GetStatus(ctx, userID)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if status.CurrentStreak != 1 {
		t.Errorf("CurrentStreak = %d, want 1 (unchanged by the repeated same-day call)", status.CurrentStreak)
	}
	if len(status.PublishedDates) != 1 {
		t.Errorf("PublishedDates = %v, want exactly 1 entry, not a duplicate", status.PublishedDates)
	}
}

func TestRecordPublish_ConsecutiveDayIncrements(t *testing.T) {
	const userID = 999999103
	svc, db := newTestService(t, userID)
	backdateStreak(t, db, userID, 4, daysAgo(1))

	if _, err := svc.RecordPublish(context.Background(), userID); err != nil {
		t.Fatalf("RecordPublish: %v", err)
	}
	status, err := svc.GetStatus(context.Background(), userID)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if status.CurrentStreak != 5 {
		t.Errorf("CurrentStreak = %d, want 5 (4 + today)", status.CurrentStreak)
	}
}

func TestRecordPublish_GapResetsStreak(t *testing.T) {
	const userID = 999999104
	svc, db := newTestService(t, userID)
	backdateStreak(t, db, userID, 10, daysAgo(5)) // missed several days

	if _, err := svc.RecordPublish(context.Background(), userID); err != nil {
		t.Fatalf("RecordPublish: %v", err)
	}
	status, err := svc.GetStatus(context.Background(), userID)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if status.CurrentStreak != 1 {
		t.Errorf("CurrentStreak = %d, want 1 (streak broke, rebuilds from scratch)", status.CurrentStreak)
	}
}

func TestGetStatus_LapsedStreakReadsAsZeroWithoutMutating(t *testing.T) {
	const userID = 999999105
	svc, db := newTestService(t, userID)
	backdateStreak(t, db, userID, 7, daysAgo(3)) // neither today nor yesterday

	status, err := svc.GetStatus(context.Background(), userID)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if status.CurrentStreak != 0 {
		t.Errorf("CurrentStreak = %d, want 0 (lapsed)", status.CurrentStreak)
	}

	// GetStatus is a pure read — the underlying row must be untouched, so a
	// publish made right after this still resumes correctly rather than
	// double-resetting.
	var stored int
	if err := db.QueryRow(`SELECT current_streak FROM community_streaks WHERE user_id = ?`, userID).Scan(&stored); err != nil {
		t.Fatalf("read raw row: %v", err)
	}
	if stored != 7 {
		t.Errorf("stored current_streak = %d, want unchanged 7 (GetStatus must not write)", stored)
	}
}

func TestRecordPublish_MilestoneAt3DaysGrantsCredits(t *testing.T) {
	const userID = 999999106
	svc, db := newTestService(t, userID)
	backdateStreak(t, db, userID, 2, daysAgo(1))

	awarded, err := svc.RecordPublish(context.Background(), userID)
	if err != nil {
		t.Fatalf("RecordPublish: %v", err)
	}
	if len(awarded) != 1 || awarded[0].Days != 3 || awarded[0].Credits != 10 {
		t.Fatalf("awarded = %v, want exactly [{3 10}]", awarded)
	}

	var balance int
	if err := db.QueryRow(`SELECT balance FROM credit_accounts WHERE user_id = ?`, userID).Scan(&balance); err != nil {
		t.Fatalf("read balance: %v", err)
	}
	if balance != 10 {
		t.Errorf("balance = %d, want 10", balance)
	}
}

// TestRecordPublish_MonthlyCapBlocksFurtherGrants seeds 4 existing 3-day
// reward rows this month directly (the monthlyCap=4 ceiling) and confirms a
// 5th crossing this month grants nothing further — RecordPublish's cap
// check counts existing community_streak_rewards rows, so this exercises
// that count without needing 4 real streak-break-and-rebuild cycles.
func TestRecordPublish_MonthlyCapBlocksFurtherGrants(t *testing.T) {
	const userID = 999999107
	svc, db := newTestService(t, userID)
	today := daysAgo(0)
	for i := 0; i < 4; i++ {
		if _, err := db.Exec(`INSERT INTO community_streak_rewards (user_id, milestone, credits, awarded_date) VALUES (?, 3, 10, ?)`,
			userID, today); err != nil {
			t.Fatalf("seed reward %d: %v", i, err)
		}
	}
	backdateStreak(t, db, userID, 2, daysAgo(1))

	awarded, err := svc.RecordPublish(context.Background(), userID)
	if err != nil {
		t.Fatalf("RecordPublish: %v", err)
	}
	if len(awarded) != 0 {
		t.Errorf("awarded = %v, want none — the 3-day milestone's monthly cap (4) was already reached", awarded)
	}
}

func TestGetStatus_PublishedDatesWindow(t *testing.T) {
	const userID = 999999108
	svc, db := newTestService(t, userID)

	inWindow := daysAgo(10)
	outOfWindow := daysAgo(historyDays + 5)
	for _, d := range []string{inWindow, outOfWindow} {
		if _, err := db.Exec(`INSERT INTO community_publish_log (user_id, publish_date) VALUES (?, ?)`, userID, d); err != nil {
			t.Fatalf("seed publish_log %s: %v", d, err)
		}
	}

	status, err := svc.GetStatus(context.Background(), userID)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if len(status.PublishedDates) != 1 || status.PublishedDates[0] != inWindow {
		t.Errorf("PublishedDates = %v, want exactly [%s] (the out-of-window date must be excluded)", status.PublishedDates, inWindow)
	}
}

func TestGetStatus_FreshUserHasEmptyNonNilPublishedDates(t *testing.T) {
	const userID = 999999109
	svc, _ := newTestService(t, userID)

	status, err := svc.GetStatus(context.Background(), userID)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if status.PublishedDates == nil {
		t.Error("PublishedDates is nil, want a non-nil empty slice (marshals to [] not null)")
	}
	if len(status.PublishedDates) != 0 {
		t.Errorf("PublishedDates = %v, want empty for a fresh user", status.PublishedDates)
	}
}
