// Package orchestrator runs job plans. MySQL rows in job_nodes are the only
// source of execution state and every transition is a compare-and-set, so
// any number of api and worker processes can run side by side: there is no
// leader and no in-memory state that a crash could lose. Redis (asynq)
// merely carries "node N is ready" messages; a lost message is re-sent by the
// sweeper, a duplicated one is rejected by the CAS.
//
// Lock order is always jobs row, then job_nodes rows, then credit rows, which
// keeps transactions that touch the same job deadlock-free.
package orchestrator

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	"aigc-platform/internal/infra/executor/spi/executor"
	"aigc-platform/internal/pkg/id"
)

// Queue names. Each queue is served by its own worker pool so one kind of
// work can never starve another.
const (
	QueueInteractive = "v2:interactive"
	QueueVideo       = "v2:video"
	QueueMedia       = "v2:media"
	QueueSystem      = "v2:system"
)

// QueueFor maps an executor type to its queue.
func QueueFor(executorType string) string {
	switch {
	case strings.HasPrefix(executorType, "local."):
		return QueueMedia
	case executorType == "minimax.video", executorType == "minimax.video.regen", executorType == "minimax.prompt_enhance":
		return QueueVideo
	default:
		return QueueInteractive
	}
}

// JobRef identifies the job a plan belongs to.
type JobRef struct {
	ID     uint64
	BizID  string
	UserID uint64
}

// Dispatcher delivers work to workers.
type Dispatcher interface {
	// Enqueue schedules a node run or poll. Implementations must treat a
	// duplicate TaskID as success.
	Enqueue(ctx context.Context, t Task) error
}

// Task is one unit of worker work.
type Task struct {
	Kind      string // TaskRun | TaskPoll | a follow-up type
	NodeID    uint64
	Seq       int
	Queue     string
	ProcessAt time.Time
	Timeout   time.Duration
	Payload   []byte // follow-ups only
	UniqueID  string // follow-ups only; empty means no de-duplication
	// Nudge asks for an immediate extra poll next to the scheduled one.
	Nudge bool
}

const (
	TaskRun  = "node:run"
	TaskPoll = "node:poll"
)

func (t Task) ID() string {
	switch t.Kind {
	case TaskRun:
		return fmt.Sprintf("run:%d:%d", t.NodeID, t.Seq)
	case TaskPoll:
		if t.Nudge {
			return fmt.Sprintf("poll:%d:%d:nudge", t.NodeID, t.Seq)
		}
		return fmt.Sprintf("poll:%d:%d", t.NodeID, t.Seq)
	default:
		return t.UniqueID
	}
}

// Publisher pushes events and cancellation signals to other processes.
type Publisher interface {
	PublishEvents(ctx context.Context, events []Event) error
	PublishCancel(ctx context.Context, nodeIDs []uint64) error
}

// Billing settles credits inside the orchestrator's transactions.
type Billing interface {
	// CommitTx charges costYuan for one execution attempt of a node and
	// returns the credits charged.
	CommitTx(ctx context.Context, tx *sql.Tx, job JobRef, node string, attempt int, costYuan float64) (int, error)
	// ReleaseTx returns whatever is left of the job's reservation.
	ReleaseTx(ctx context.Context, tx *sql.Tx, job JobRef) (int, error)
}

// Hooks lets the application react to finished nodes and jobs inside the
// same transaction. Returned tasks are dispatched after commit.
type Hooks interface {
	NodeFinishedTx(ctx context.Context, tx *sql.Tx, job JobRef, node FinishedNode) ([]Task, error)
	JobFinishedTx(ctx context.Context, tx *sql.Tx, job JobRef, status string) error
}

// FinishedNode describes a node execution that just ended.
type FinishedNode struct {
	ID        uint64
	Name      string
	TaskRunID string
	Executor  string
	Status    string
	Message   string
	Outputs   map[string]any
	Attempt   int
}

// Config tunes timing. Zero values take the defaults below.
type Config struct {
	WorkerID        string
	Lease           time.Duration
	DefaultDeadline time.Duration
	GateTTL         time.Duration
	ReadyStaleAfter time.Duration
	PollInterval    time.Duration
	MaxPollInterval time.Duration
	RetryBase       time.Duration
	MaxPollErrors   int
	SweepBatch      int
	EventRetention  time.Duration
}

func (c Config) withDefaults() Config {
	if c.WorkerID == "" {
		host, _ := os.Hostname()
		c.WorkerID = fmt.Sprintf("%s-%d-%s", host, os.Getpid(), id.New()[20:])
	}
	if c.Lease <= 0 {
		c.Lease = 60 * time.Second
	}
	if c.DefaultDeadline <= 0 {
		c.DefaultDeadline = 2 * time.Hour
	}
	if c.GateTTL <= 0 {
		c.GateTTL = 7 * 24 * time.Hour
	}
	if c.ReadyStaleAfter <= 0 {
		c.ReadyStaleAfter = 30 * time.Second
	}
	if c.PollInterval <= 0 {
		c.PollInterval = 10 * time.Second
	}
	if c.MaxPollInterval <= 0 {
		c.MaxPollInterval = 30 * time.Second
	}
	if c.RetryBase <= 0 {
		c.RetryBase = 5 * time.Second
	}
	if c.MaxPollErrors <= 0 {
		c.MaxPollErrors = 10
	}
	if c.SweepBatch <= 0 {
		c.SweepBatch = 100
	}
	if c.EventRetention <= 0 {
		c.EventRetention = 7 * 24 * time.Hour
	}
	return c
}

// Orchestrator is safe for concurrent use and holds no job state in memory.
type Orchestrator struct {
	db       *sql.DB
	dispatch Dispatcher
	pub      Publisher
	billing  Billing
	hooks    Hooks
	plugins  *executor.Registry
	cfg      Config
	now      func() time.Time
	runner   *runnerState
}

// Options wires collaborators; Plugins is only needed in worker processes.
type Options struct {
	DB         *sql.DB
	Dispatcher Dispatcher
	Publisher  Publisher
	Billing    Billing
	Hooks      Hooks
	Plugins    *executor.Registry
	Config     Config
	Now        func() time.Time
}

func New(o Options) *Orchestrator {
	now := o.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Orchestrator{
		db: o.DB, dispatch: o.Dispatcher, pub: o.Publisher, billing: o.Billing, hooks: o.Hooks,
		plugins: o.Plugins, cfg: o.Config.withDefaults(), now: now, runner: newRunnerState(),
	}
}

// Config returns the effective configuration.
func (o *Orchestrator) Config() Config { return o.cfg }
