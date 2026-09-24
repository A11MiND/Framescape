package orchestrator

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"aigc-platform/internal/domain/workflow"
	"aigc-platform/internal/infra/executor/spi/executor"
	"aigc-platform/internal/infra/executor/spi/model"
	"aigc-platform/internal/pkg/config"
	"aigc-platform/internal/pkg/id"
)

// Reserved test-only user ids; rows are removed by t.Cleanup.
const testUserBase = 999998000

var testUserSeq atomic.Uint64

type testClock struct{ ns atomic.Int64 }

func (c *testClock) now() time.Time          { return time.Unix(0, c.ns.Load()).UTC() }
func (c *testClock) advance(d time.Duration) { c.ns.Add(int64(d)) }
func (c *testClock) set(t time.Time) {
	if t.UnixNano() > c.ns.Load() {
		c.ns.Store(t.UnixNano())
	}
}

// memDispatcher mimics asynq: a TaskID is unique while the task is pending.
type memDispatcher struct {
	mu      sync.Mutex
	pending map[string]Task
	history []Task
	drop    atomic.Bool
}

func (d *memDispatcher) Enqueue(_ context.Context, t Task) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.history = append(d.history, t)
	if d.drop.Load() {
		return nil
	}
	if _, dup := d.pending[t.ID()]; dup {
		return nil
	}
	d.pending[t.ID()] = t
	return nil
}

func (d *memDispatcher) pop(now time.Time) (Task, bool, time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var due []Task
	var next time.Time
	for _, t := range d.pending {
		if t.ProcessAt.IsZero() || !t.ProcessAt.After(now) {
			due = append(due, t)
		} else if next.IsZero() || t.ProcessAt.Before(next) {
			next = t.ProcessAt
		}
	}
	if len(due) == 0 {
		return Task{}, false, next
	}
	sort.Slice(due, func(i, j int) bool { return due[i].ID() < due[j].ID() })
	t := due[0]
	delete(d.pending, t.ID())
	return t, true, next
}

type memPublisher struct {
	mu     sync.Mutex
	events []Event
	fail   atomic.Bool
	orch   *Orchestrator
}

func (p *memPublisher) PublishEvents(_ context.Context, events []Event) error {
	if p.fail.Load() {
		return fmt.Errorf("publish failed")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, events...)
	return nil
}

func (p *memPublisher) PublishCancel(_ context.Context, ids []uint64) error {
	if p.orch != nil {
		p.orch.CancelLocal(ids)
	}
	return nil
}

func (p *memPublisher) types(jobBizID string) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for _, e := range p.events {
		if e.JobBizID == jobBizID {
			out = append(out, e.Type)
		}
	}
	return out
}

type memBilling struct {
	mu       sync.Mutex
	commits  map[string]float64
	releases map[uint64]int
}

func (b *memBilling) CommitTx(_ context.Context, _ *sql.Tx, job JobRef, node string, attempt int, cost float64) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.commits[fmt.Sprintf("%s/%s/%d", job.BizID, node, attempt)] += cost
	return int(cost * 10), nil
}

func (b *memBilling) ReleaseTx(_ context.Context, _ *sql.Tx, job JobRef) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.releases[job.ID]++
	return 1, nil
}

type harness struct {
	t     *testing.T
	ctx   context.Context
	db    *sql.DB
	orch  *Orchestrator
	disp  *memDispatcher
	pub   *memPublisher
	bill  *memBilling
	clock *testClock
	reg   *executor.Registry
	fx    *fixtures
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	db, err := sql.Open("mysql", config.MySQLDSN())
	if err != nil {
		t.Fatalf("open mysql: %v", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		t.Skipf("no MySQL available: %v", err)
	}
	var hasTable int
	if err := db.QueryRow(`SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = 'job_events'`).Scan(&hasTable); err != nil || hasTable == 0 {
		db.Close()
		t.Skip("orchestrator tables not migrated")
	}
	t.Cleanup(func() { db.Close() })

	h := &harness{t: t, ctx: context.Background(), db: db, clock: &testClock{}, reg: executor.NewRegistry()}
	h.clock.set(time.Now().UTC().Truncate(time.Millisecond))
	h.disp = &memDispatcher{pending: map[string]Task{}}
	h.pub = &memPublisher{}
	h.bill = &memBilling{commits: map[string]float64{}, releases: map[uint64]int{}}
	h.fx = newFixtures()
	for _, p := range h.fx.plugins() {
		if err := h.reg.Register(p); err != nil {
			t.Fatal(err)
		}
	}
	h.orch = New(Options{
		DB: db, Dispatcher: h.disp, Publisher: h.pub, Billing: h.bill, Plugins: h.reg,
		Config: Config{WorkerID: "test-worker-" + id.New()[20:], Lease: 30 * time.Second, RetryBase: time.Second},
		Now:    h.clock.now,
	})
	h.pub.orch = h.orch
	return h
}

// newJob inserts a jobs row and submits plan.
func (h *harness) submit(plan *workflow.Plan) JobRef {
	h.t.Helper()
	userID := testUserBase + testUserSeq.Add(1)
	job := JobRef{BizID: id.New(), UserID: userID}
	h.t.Cleanup(func() {
		_, _ = h.db.Exec(`DELETE FROM job_nodes WHERE job_id = ?`, job.ID)
		_, _ = h.db.Exec(`DELETE FROM job_events WHERE user_id = ?`, userID)
		_, _ = h.db.Exec(`DELETE FROM jobs WHERE user_id = ?`, userID)
	})
	var p *Pending
	err := withTx(h.ctx, h.db, func(tx *sql.Tx) error {
		res, err := tx.Exec(`INSERT INTO jobs (biz_id, user_id, workflow_name, status, spec) VALUES (?, ?, 'test.flow', 'queued', '{}')`, job.BizID, userID)
		if err != nil {
			return err
		}
		jobID, _ := res.LastInsertId()
		job.ID = uint64(jobID)
		p, err = h.orch.SubmitTx(h.ctx, tx, job, plan)
		return err
	})
	if err != nil {
		h.t.Fatalf("submit: %v", err)
	}
	h.orch.Flush(h.ctx, p)
	return job
}

// drain runs queued work, jumping the clock forward to delayed tasks, until
// nothing is left or max steps ran.
func (h *harness) drain(max int) {
	h.t.Helper()
	for i := 0; i < max; i++ {
		t, ok, next := h.disp.pop(h.clock.now())
		if !ok {
			if next.IsZero() {
				return
			}
			h.clock.set(next)
			continue
		}
		var err error
		switch t.Kind {
		case TaskRun:
			err = h.orch.RunNode(h.ctx, t.NodeID, t.Seq)
		case TaskPoll:
			err = h.orch.PollNode(h.ctx, t.NodeID, t.Seq)
		}
		if err != nil {
			h.t.Fatalf("%s %d: %v", t.Kind, t.NodeID, err)
		}
	}
	h.t.Fatalf("drain did not finish within %d steps", max)
}

func (h *harness) jobStatus(job JobRef) (status, errCode string) {
	h.t.Helper()
	if err := h.db.QueryRow(`SELECT status, error_code FROM jobs WHERE id = ?`, job.ID).Scan(&status, &errCode); err != nil {
		h.t.Fatal(err)
	}
	return
}

func (h *harness) node(job JobRef, name string) *Node {
	h.t.Helper()
	nodes, err := h.orch.Nodes(h.ctx, job.ID)
	if err != nil {
		h.t.Fatal(err)
	}
	for _, n := range nodes {
		if n.Name == name {
			return n
		}
	}
	h.t.Fatalf("node %q not found", name)
	return nil
}

// fixtures are executors with scripted behavior.
type fixtures struct {
	mu        sync.Mutex
	calls     map[string]int
	failFirst map[string]int
	polls     map[string]int
	submits   atomic.Int64
	block     chan struct{}
}

func newFixtures() *fixtures {
	return &fixtures{calls: map[string]int{}, failFirst: map[string]int{}, polls: map[string]int{}, block: make(chan struct{})}
}

func (f *fixtures) callsOf(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[key]
}

func (f *fixtures) count(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[key]++
	return f.calls[key]
}

func (f *fixtures) plugins() []executor.Plugin {
	return []executor.Plugin{
		fnPlugin{"t.ok", func(ctx context.Context, req *executor.ExecuteRequest) (*model.ExecOutputs, error) {
			f.count(req.TaskRunID)
			in := model.ParamsToMap(req.Inputs.Parameters)
			cost, _ := in["cost"].(float64)
			return executor.OutputFrom(struct {
				AssetID string         `json:"asset-id"`
				Echo    map[string]any `json:"echo"`
				Cost    float64        `json:"cost-yuan"`
			}{AssetID: "asset-" + req.TaskName, Echo: in, Cost: cost})
		}},
		fnPlugin{"t.flaky", func(ctx context.Context, req *executor.ExecuteRequest) (*model.ExecOutputs, error) {
			n := f.count(req.TaskRunID)
			in := model.ParamsToMap(req.Inputs.Parameters)
			failures, _ := in["failures"].(float64)
			if n <= int(failures) {
				return &model.ExecOutputs{Code: model.ExecCodeError, Message: "transient"}, nil
			}
			return executor.OutputFrom(struct {
				AssetID string `json:"asset-id"`
			}{AssetID: "asset-" + req.TaskName})
		}},
		fnPlugin{"t.reject", func(ctx context.Context, req *executor.ExecuteRequest) (*model.ExecOutputs, error) {
			f.count(req.TaskRunID)
			out, _ := executor.OutputFrom(struct {
				Cost float64 `json:"cost-yuan"`
			}{Cost: 0.5})
			out.Code, out.Message = model.ExecCodeFailed, "sensitive_content: blocked"
			return out, nil
		}},
		fnPlugin{"t.nocap", func(ctx context.Context, req *executor.ExecuteRequest) (*model.ExecOutputs, error) {
			if f.count(req.TaskRunID) == 1 {
				return nil, &executor.NoCapacityError{Reason: "capacity", RetryAfter: 3 * time.Second}
			}
			return executor.OutputFrom(struct {
				AssetID string `json:"asset-id"`
			}{AssetID: "asset-" + req.TaskName})
		}},
		fnPlugin{"t.partial", func(ctx context.Context, req *executor.ExecuteRequest) (*model.ExecOutputs, error) {
			return executor.OutputFrom(struct {
				AssetIDs     []string `json:"asset-ids"`
				SuccessCount int      `json:"success-count"`
				RequestedN   int      `json:"requested-n"`
			}{AssetIDs: []string{"a1"}, SuccessCount: 1, RequestedN: 2})
		}},
		fnPlugin{"t.block", func(ctx context.Context, req *executor.ExecuteRequest) (*model.ExecOutputs, error) {
			f.count(req.TaskRunID)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-f.block:
				return executor.OutputFrom(struct {
					AssetID string `json:"asset-id"`
				}{AssetID: "late"})
			}
		}},
		&asyncFixture{f: f},
	}
}

type fnPlugin struct {
	typ string
	fn  func(context.Context, *executor.ExecuteRequest) (*model.ExecOutputs, error)
}

func (p fnPlugin) Type() string                 { return p.typ }
func (p fnPlugin) Schema() model.ExecutorSchema { return model.ExecutorSchema{Type: p.typ} }
func (p fnPlugin) Execute(ctx context.Context, req *executor.ExecuteRequest) (*model.ExecOutputs, error) {
	return p.fn(ctx, req)
}

// asyncFixture finishes after `polls` polls (input), 2 by default.
type asyncFixture struct{ f *fixtures }

func (a *asyncFixture) Type() string                 { return "t.async" }
func (a *asyncFixture) Schema() model.ExecutorSchema { return model.ExecutorSchema{Type: "t.async"} }
func (a *asyncFixture) Execute(context.Context, *executor.ExecuteRequest) (*model.ExecOutputs, error) {
	return nil, fmt.Errorf("async only")
}
func (a *asyncFixture) Submit(_ context.Context, req *executor.ExecuteRequest) (executor.ProviderRef, *model.ExecOutputs, error) {
	a.f.submits.Add(1)
	return executor.ProviderRef{Provider: "fake", TaskID: "remote-" + req.TaskRunID}, nil, nil
}
func (a *asyncFixture) Poll(_ context.Context, req *executor.ExecuteRequest, ref executor.ProviderRef) (executor.PollResult, error) {
	a.f.mu.Lock()
	a.f.polls[ref.TaskID]++
	n := a.f.polls[ref.TaskID]
	a.f.mu.Unlock()
	want := 2.0
	if v, ok := model.ParamsToMap(req.Inputs.Parameters)["polls"].(float64); ok {
		want = v
	}
	if float64(n) < want {
		return executor.PollResult{After: 10 * time.Second}, nil
	}
	out, _ := executor.OutputFrom(struct {
		AssetID string  `json:"asset-id"`
		Cost    float64 `json:"cost-yuan"`
	}{AssetID: "video-" + req.TaskName, Cost: 2.5})
	return executor.PollResult{Done: true, Outputs: out}, nil
}
