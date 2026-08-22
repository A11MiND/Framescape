package projection

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/redis/go-redis/v9"

	"github.com/BabySid/aether/model"
	"github.com/BabySid/aether/store"

	"aigc-platform/internal/application/creditsvc"
	"aigc-platform/internal/pkg/config"
	"aigc-platform/internal/pkg/id"
)

// testEnv bundles what most tests in this file need: a real MySQL
// connection (skip if unreachable, same convention every other DB-backed
// package's tests use) and a seeded jobs row with a matching zero-balance
// credit_accounts row, cleaned up on test completion. Redis stays optional —
// only tests that actually call publish/onTaskRun/onWorkflowRun need it.
type testEnv struct {
	db      *sql.DB
	credits *creditsvc.Service
	jobID   uint64
	bizID   string
	userID  uint64
	runID   string
}

func newTestEnv(t *testing.T, held int) *testEnv {
	t.Helper()
	db, err := sql.Open("mysql", config.MySQLDSN())
	if err != nil {
		t.Fatalf("open mysql: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Skipf("no local MySQL available, skipping: %v", err)
	}

	userID := uint64(999999200 + time.Now().UnixNano()%1000)
	bizID := id.New()
	runID := id.New()

	cleanup := func() {
		db.Exec(`DELETE FROM job_nodes WHERE job_id IN (SELECT id FROM jobs WHERE biz_id = ?)`, bizID)
		db.Exec(`DELETE FROM moderation_records WHERE job_id IN (SELECT id FROM jobs WHERE biz_id = ?)`, bizID)
		db.Exec(`DELETE FROM jobs WHERE biz_id = ?`, bizID)
		db.Exec(`DELETE FROM credit_accounts WHERE user_id = ?`, userID)
		db.Exec(`DELETE FROM credit_ledger WHERE user_id = ?`, userID)
	}
	cleanup()
	t.Cleanup(func() { cleanup(); db.Close() })

	if _, err := db.Exec(`INSERT INTO credit_accounts (user_id, balance, held) VALUES (?, 0, ?)`, userID, held); err != nil {
		t.Fatalf("seed credit_accounts: %v", err)
	}
	res, err := db.Exec(`
		INSERT INTO jobs (biz_id, user_id, workflow_name, workflow_run_id, status, spec, credit_held)
		VALUES (?, ?, 'image.single', ?, 'running', '{}', ?)`,
		bizID, userID, runID, held)
	if err != nil {
		t.Fatalf("seed jobs: %v", err)
	}
	jobID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("get job id: %v", err)
	}

	return &testEnv{db: db, credits: creditsvc.New(db), jobID: uint64(jobID), bizID: bizID, userID: userID, runID: runID}
}

func testRedisClient(t *testing.T) *redis.Client {
	t.Helper()
	c := redis.NewClient(&redis.Options{Addr: config.RedisAddr()})
	if err := c.Ping(context.Background()).Err(); err != nil {
		t.Skip("no local Redis available, skipping")
	}
	return c
}

func phasePtr(s string) *model.Phase { p := model.Phase(s); return &p }

func TestIsTerminalPhase(t *testing.T) {
	for _, p := range []string{"Succeeded", "Failed", "Error", "Timeout", "Cancelled"} {
		if !isTerminalPhase(p) {
			t.Errorf("isTerminalPhase(%q) = false, want true", p)
		}
	}
	for _, p := range []string{"Created", "Ready", "Running", "Suspended", ""} {
		if isTerminalPhase(p) {
			t.Errorf("isTerminalPhase(%q) = true, want false", p)
		}
	}
}

func TestMaybeCommitCredits(t *testing.T) {
	env := newTestEnv(t, 100)
	p := New(env.db, nil, env.credits, nil, nil)
	ctx := context.Background()

	taskRunID := id.New()
	if _, err := env.db.Exec(`
		INSERT INTO job_nodes (job_id, task_run_id, node_name, executor_type, phase) VALUES (?, ?, 'gen', 'minimax.image', 'Running')`,
		env.jobID, taskRunID); err != nil {
		t.Fatalf("seed job_nodes: %v", err)
	}

	tr := &store.TaskRun{
		RunID: taskRunID, WorkflowRunID: env.runID, TaskName: "gen", Status: phasePtr("Succeeded"),
		Outputs: &model.Outputs{ExecOutputs: model.ExecOutputs{Parameters: []model.Parameter{
			{Name: "cost-yuan", Value: []byte("0.025")},
		}}},
	}
	p.maybeCommitCredits(ctx, env.jobID, tr)

	var settled, nodeCost int
	if err := env.db.QueryRow(`SELECT credit_settled FROM jobs WHERE id = ?`, env.jobID).Scan(&settled); err != nil {
		t.Fatalf("read jobs.credit_settled: %v", err)
	}
	if settled != 1 { // ceil(0.025*2/0.1) = 1, creditsvc's own §12.2 floor
		t.Errorf("credit_settled = %d, want 1", settled)
	}
	if err := env.db.QueryRow(`SELECT credit_cost FROM job_nodes WHERE task_run_id = ?`, taskRunID).Scan(&nodeCost); err != nil {
		t.Fatalf("read job_nodes.credit_cost: %v", err)
	}
	if nodeCost != 1 {
		t.Errorf("job_nodes.credit_cost = %d, want 1", nodeCost)
	}

	var held int
	if err := env.db.QueryRow(`SELECT held FROM credit_accounts WHERE user_id = ?`, env.userID).Scan(&held); err != nil {
		t.Fatalf("read credit_accounts.held: %v", err)
	}
	if held != 99 {
		t.Errorf("held = %d, want 99 (100 - 1 committed)", held)
	}
}

func TestMaybeCommitCredits_NoCostYuanIsNoOp(t *testing.T) {
	env := newTestEnv(t, 100)
	p := New(env.db, nil, env.credits, nil, nil)

	tr := &store.TaskRun{
		RunID: id.New(), WorkflowRunID: env.runID, TaskName: "gate", Status: phasePtr("Succeeded"),
		Outputs: &model.Outputs{ExecOutputs: model.ExecOutputs{Parameters: []model.Parameter{
			{Name: "some-other-output", Value: []byte(`"x"`)},
		}}},
	}
	p.maybeCommitCredits(context.Background(), env.jobID, tr) // must not panic or touch the account

	var settled int
	env.db.QueryRow(`SELECT credit_settled FROM jobs WHERE id = ?`, env.jobID).Scan(&settled)
	if settled != 0 {
		t.Errorf("credit_settled = %d, want 0 (no cost-yuan output, nothing to commit)", settled)
	}
}

func TestMaybeCommitCredits_NotSucceededIsNoOp(t *testing.T) {
	env := newTestEnv(t, 100)
	p := New(env.db, nil, env.credits, nil, nil)

	tr := &store.TaskRun{
		RunID: id.New(), WorkflowRunID: env.runID, TaskName: "gen", Status: phasePtr("Running"),
		Outputs: &model.Outputs{ExecOutputs: model.ExecOutputs{Parameters: []model.Parameter{
			{Name: "cost-yuan", Value: []byte("0.025")},
		}}},
	}
	p.maybeCommitCredits(context.Background(), env.jobID, tr)

	var settled int
	env.db.QueryRow(`SELECT credit_settled FROM jobs WHERE id = ?`, env.jobID).Scan(&settled)
	if settled != 0 {
		t.Errorf("credit_settled = %d, want 0 (task not Succeeded yet)", settled)
	}
}

func TestMaybeRecordModeration_SensitiveContentPrefix(t *testing.T) {
	env := newTestEnv(t, 100)
	p := New(env.db, nil, env.credits, nil, nil)
	taskRunID := id.New()

	tr := &store.TaskRun{
		RunID: taskRunID, WorkflowRunID: env.runID, TaskName: "gen", Status: phasePtr("Failed"),
		Outputs: &model.Outputs{ExecOutputs: model.ExecOutputs{Message: "sensitive_content: 涉及敏感内容"}},
	}
	p.maybeRecordModeration(context.Background(), env.jobID, tr)

	var msg string
	if err := env.db.QueryRow(`SELECT provider_message FROM moderation_records WHERE task_run_id = ?`, taskRunID).Scan(&msg); err != nil {
		t.Fatalf("expected a moderation_records row, query failed: %v", err)
	}
	if msg != "sensitive_content: 涉及敏感内容" {
		t.Errorf("provider_message = %q, want the full message preserved", msg)
	}

	// Idempotent — a duplicate delivery for the same task_run_id must not error.
	p.maybeRecordModeration(context.Background(), env.jobID, tr)
	var count int
	env.db.QueryRow(`SELECT COUNT(*) FROM moderation_records WHERE task_run_id = ?`, taskRunID).Scan(&count)
	if count != 1 {
		t.Errorf("moderation_records rows = %d, want exactly 1 after a duplicate delivery", count)
	}
}

func TestMaybeRecordModeration_OtherFailuresIgnored(t *testing.T) {
	env := newTestEnv(t, 100)
	p := New(env.db, nil, env.credits, nil, nil)
	taskRunID := id.New()

	tr := &store.TaskRun{
		RunID: taskRunID, WorkflowRunID: env.runID, TaskName: "gen", Status: phasePtr("Failed"),
		Outputs: &model.Outputs{ExecOutputs: model.ExecOutputs{Message: "rate_limited: too many requests"}},
	}
	p.maybeRecordModeration(context.Background(), env.jobID, tr)

	var count int
	env.db.QueryRow(`SELECT COUNT(*) FROM moderation_records WHERE task_run_id = ?`, taskRunID).Scan(&count)
	if count != 0 {
		t.Errorf("moderation_records rows = %d, want 0 (F8.4 only records sensitive_content rejections)", count)
	}
}

func TestMaybeRefundCredits(t *testing.T) {
	env := newTestEnv(t, 100)
	p := New(env.db, nil, env.credits, nil, nil)

	// Simulate: 100 held, 30 already committed via a real Commit call (so the
	// balance+held==ledger invariant this whole system relies on stays real)
	// — 1.5 yuan is exactly ceil(1.5*MARGIN_K/0.1) = 30 credits, matching
	// jobs.credit_settled below precisely rather than by coincidence.
	committed, err := env.credits.Commit(context.Background(), env.userID, "test:commit:1", "tr-1", 1.5)
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if committed != 30 {
		t.Fatalf("Commit returned %d, want 30 — fix this test's costYuan input", committed)
	}
	if _, err := env.db.Exec(`UPDATE jobs SET credit_settled = ? WHERE id = ?`, committed, env.jobID); err != nil {
		t.Fatalf("update credit_settled: %v", err)
	}

	p.maybeRefundCredits(context.Background(), env.runID)

	var balance, held int
	if err := env.db.QueryRow(`SELECT balance, held FROM credit_accounts WHERE user_id = ?`, env.userID).Scan(&balance, &held); err != nil {
		t.Fatalf("read credit_accounts: %v", err)
	}
	if held != 0 {
		t.Errorf("held = %d, want 0 (refund clears the remainder)", held)
	}
	if balance != 70 {
		t.Errorf("balance = %d, want 70 (100 held - 30 settled = 70 refunded)", balance)
	}

	// Idempotent — a duplicate terminal delivery for the same run must not
	// refund twice (credit_ledger's own uk_idem on "job:{bizID}:refund").
	p.maybeRefundCredits(context.Background(), env.runID)
	env.db.QueryRow(`SELECT balance FROM credit_accounts WHERE user_id = ?`, env.userID).Scan(&balance)
	if balance != 70 {
		t.Errorf("balance after duplicate refund call = %d, want unchanged 70", balance)
	}
}

func TestMaybeRefundCredits_NothingHeldIsNoOp(t *testing.T) {
	env := newTestEnv(t, 0)
	p := New(env.db, nil, env.credits, nil, nil)
	p.maybeRefundCredits(context.Background(), env.runID) // must not error or touch the ledger

	var balance int
	env.db.QueryRow(`SELECT balance FROM credit_accounts WHERE user_id = ?`, env.userID).Scan(&balance)
	if balance != 0 {
		t.Errorf("balance = %d, want 0 (nothing was held, nothing to refund)", balance)
	}
}

// TestUpsertJobNode_TimestampsNeverClobbered is upsertJobNode's own
// documented invariant: started_at/finished_at are set once, on whichever
// delivery first makes them true, and a later (possibly out-of-order)
// delivery for the same task_run_id must never overwrite an
// already-recorded timestamp — the COALESCE on the UPDATE side is what
// this test actually exercises.
func TestUpsertJobNode_TimestampsNeverClobbered(t *testing.T) {
	env := newTestEnv(t, 100)
	p := New(env.db, nil, env.credits, nil, nil)
	taskRunID := id.New()

	running := &store.TaskRun{RunID: taskRunID, WorkflowRunID: env.runID, TaskName: "gen", Scope: "main/", Status: phasePtr("Running")}
	if err := p.upsertJobNode(context.Background(), env.jobID, running); err != nil {
		t.Fatalf("upsert (Running): %v", err)
	}
	var firstStarted sql.NullTime
	env.db.QueryRow(`SELECT started_at FROM job_nodes WHERE task_run_id = ?`, taskRunID).Scan(&firstStarted)
	if !firstStarted.Valid {
		t.Fatal("started_at not set after the Running delivery")
	}

	time.Sleep(10 * time.Millisecond) // ensure a real clock difference to detect any clobber
	succeeded := &store.TaskRun{RunID: taskRunID, WorkflowRunID: env.runID, TaskName: "gen", Scope: "main/", Status: phasePtr("Succeeded")}
	if err := p.upsertJobNode(context.Background(), env.jobID, succeeded); err != nil {
		t.Fatalf("upsert (Succeeded): %v", err)
	}

	var phase string
	var startedAt, finishedAt sql.NullTime
	if err := env.db.QueryRow(`SELECT phase, started_at, finished_at FROM job_nodes WHERE task_run_id = ?`, taskRunID).
		Scan(&phase, &startedAt, &finishedAt); err != nil {
		t.Fatalf("read job_nodes: %v", err)
	}
	if phase != "Succeeded" {
		t.Errorf("phase = %q, want Succeeded (the terminal delivery)", phase)
	}
	if !finishedAt.Valid {
		t.Error("finished_at not set after the Succeeded delivery")
	}
	if !startedAt.Time.Equal(firstStarted.Time) {
		t.Errorf("started_at changed from %v to %v — the second delivery clobbered it", firstStarted.Time, startedAt.Time)
	}
}

func TestPublish(t *testing.T) {
	rdb := testRedisClient(t)
	p := New(nil, rdb, nil, nil, nil)
	runID := id.New()

	sub := rdb.Subscribe(context.Background(), ChannelForRun(runID))
	defer sub.Close()
	ch := sub.Channel()

	p.publish(context.Background(), runID, Event{Type: "node_update", Node: "gen", Phase: "Running", WorkflowRunID: runID})

	select {
	case msg := <-ch:
		var ev Event
		if err := json.Unmarshal([]byte(msg.Payload), &ev); err != nil {
			t.Fatalf("decode published event: %v", err)
		}
		if ev.Type != "node_update" || ev.Node != "gen" || ev.Phase != "Running" {
			t.Errorf("event = %+v, want {node_update gen Running}", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no message received on the subscribed channel")
	}
}

// TestOnWorkflowRun_TerminalRefundsAndPublishes exercises the real
// Callback entrypoint (not maybeRefundCredits directly) end to end,
// confirming the refund and the SSE publish both actually happen off one
// terminal OnWorkflowRun delivery.
func TestOnWorkflowRun_TerminalRefundsAndPublishes(t *testing.T) {
	env := newTestEnv(t, 50)
	rdb := testRedisClient(t)
	p := New(env.db, rdb, env.credits, nil, nil)

	sub := rdb.Subscribe(context.Background(), ChannelForRun(env.runID))
	defer sub.Close()
	ch := sub.Channel()

	p.onWorkflowRun(context.Background(), &store.WorkflowRun{RunID: env.runID, Status: phasePtr("Succeeded")})

	select {
	case msg := <-ch:
		var ev Event
		_ = json.Unmarshal([]byte(msg.Payload), &ev)
		if ev.Type != "job_update" || ev.Phase != "Succeeded" {
			t.Errorf("event = %+v, want job_update/Succeeded", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no job_update event published")
	}

	var balance int
	env.db.QueryRow(`SELECT balance FROM credit_accounts WHERE user_id = ?`, env.userID).Scan(&balance)
	if balance != 50 {
		t.Errorf("balance = %d, want 50 (fully refunded, nothing was ever committed)", balance)
	}
}
