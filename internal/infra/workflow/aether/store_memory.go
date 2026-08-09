package aetherengine

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/BabySid/aether/model"
	"github.com/BabySid/aether/store"
)

// MemoryStore is a process-local store.Store implementation, used for W1's
// skeleton (DEV_PLAN.md §5) before the MySQL-backed store lands in W2. It is
// written directly against store/store.go's documented contract (see
// docs/aether-validation-report.md §two.3), not copied from aether's own
// cmd/playground reference implementation (which is unexported, package
// main).
type MemoryStore struct {
	mu sync.Mutex

	wfRuns   map[string]*store.WorkflowRun
	taskRuns map[string]*store.TaskRun
	schemas  map[string]store.SchemaRecord // key: execType+"/"+workerID
	crons    map[string]*store.CronWorkflowRecord
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		wfRuns:   make(map[string]*store.WorkflowRun),
		taskRuns: make(map[string]*store.TaskRun),
		schemas:  make(map[string]store.SchemaRecord),
		crons:    make(map[string]*store.CronWorkflowRecord),
	}
}

func cloneWorkflowRun(r *store.WorkflowRun) *store.WorkflowRun {
	cp := *r
	return &cp
}

func cloneTaskRun(r *store.TaskRun) *store.TaskRun {
	cp := *r
	return &cp
}

// --- WorkflowRunStore ---

func (s *MemoryStore) CreateWorkflowRun(_ context.Context, run *store.WorkflowRun) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.wfRuns[run.RunID]; exists {
		return nil // idempotent create, mirrors CreateTaskRun's documented contract
	}
	cp := cloneWorkflowRun(run)
	cp.CreatedAt = time.Now()
	cp.UpdatedAt = cp.CreatedAt
	cp.Token = 1
	s.wfRuns[run.RunID] = cp
	return nil
}

func (s *MemoryStore) GetWorkflowRun(_ context.Context, runID string) (*store.WorkflowRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.wfRuns[runID]
	if !ok {
		return nil, store.ErrNotFound
	}
	return cloneWorkflowRun(r), nil
}

func (s *MemoryStore) UpdateWorkflowRun(_ context.Context, run *store.WorkflowRun) (*store.WorkflowRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.wfRuns[run.RunID]
	if !ok {
		return nil, store.ErrNotFound
	}
	if run.Token != 0 && cur.Token != run.Token {
		return nil, fmt.Errorf("workflow run %s: %w", run.RunID, store.ErrTokenMismatch)
	}
	if run.Status != nil {
		cur.Status = run.Status
	}
	if run.Message != nil {
		cur.Message = run.Message
	}
	if run.Outputs != nil {
		cur.Outputs = run.Outputs
	}
	if run.Metrics != nil {
		cur.Metrics = run.Metrics
	}
	if run.Deadline != nil {
		cur.Deadline = run.Deadline
	}
	cur.Token++
	cur.UpdatedAt = time.Now()
	return cloneWorkflowRun(cur), nil
}

func (s *MemoryStore) ListActiveWorkflowRuns(_ context.Context) ([]*store.WorkflowRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*store.WorkflowRun
	for _, r := range s.wfRuns {
		if r.Deadline == nil {
			continue
		}
		if r.Status != nil && (*r.Status).IsTerminal() {
			continue
		}
		out = append(out, cloneWorkflowRun(r))
	}
	return out, nil
}

func (s *MemoryStore) DeleteWorkflowRun(_ context.Context, runID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.wfRuns[runID]; !ok {
		return store.ErrNotFound
	}
	delete(s.wfRuns, runID)
	for id, tr := range s.taskRuns {
		if tr.WorkflowRunID == runID {
			delete(s.taskRuns, id)
		}
	}
	return nil
}

func (s *MemoryStore) ListWorkflowRunsByCronID(_ context.Context, cronWorkflowID string) ([]*store.WorkflowRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*store.WorkflowRun
	for _, r := range s.wfRuns {
		if r.CronWorkflowID == cronWorkflowID {
			out = append(out, cloneWorkflowRun(r))
		}
	}
	return out, nil
}

// --- TaskRunStore ---

func (s *MemoryStore) CreateTaskRun(_ context.Context, run *store.TaskRun) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.taskRuns {
		if existing.WorkflowRunID == run.WorkflowRunID &&
			existing.ParentRunID == run.ParentRunID &&
			existing.Scope == run.Scope &&
			existing.TaskName == run.TaskName {
			return nil // idempotent, per store.TaskRunStore.CreateTaskRun contract
		}
	}
	cp := cloneTaskRun(run)
	cp.CreatedAt = time.Now()
	cp.UpdatedAt = cp.CreatedAt
	cp.Token = 1
	s.taskRuns[run.RunID] = cp
	return nil
}

func (s *MemoryStore) GetTaskRun(_ context.Context, taskRunID string) (*store.TaskRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.taskRuns[taskRunID]
	if !ok {
		return nil, store.ErrNotFound
	}
	return cloneTaskRun(r), nil
}

func (s *MemoryStore) UpdateTaskRun(_ context.Context, run *store.TaskRun) (*store.TaskRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.taskRuns[run.RunID]
	if !ok {
		return nil, store.ErrNotFound
	}
	if run.Token != 0 && cur.Token != run.Token {
		return nil, fmt.Errorf("task run %s: %w", run.RunID, store.ErrTokenMismatch)
	}
	if run.Inputs != nil {
		cur.Inputs = run.Inputs
	}
	if run.Status != nil {
		cur.Status = run.Status
	}
	if run.Message != nil {
		cur.Message = run.Message
	}
	if run.Outputs != nil {
		cur.Outputs = run.Outputs
	}
	if run.Metrics != nil {
		cur.Metrics = run.Metrics
	}
	if run.RetryCount != nil {
		cur.RetryCount = run.RetryCount
	}
	if run.Deadline != nil {
		cur.Deadline = run.Deadline
	}
	cur.Token++
	cur.UpdatedAt = time.Now()
	return cloneTaskRun(cur), nil
}

func (s *MemoryStore) ListTaskRuns(_ context.Context, workflowRunID string) ([]*store.TaskRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*store.TaskRun
	for _, r := range s.taskRuns {
		if r.WorkflowRunID == workflowRunID {
			out = append(out, cloneTaskRun(r))
		}
	}
	return out, nil
}

func (s *MemoryStore) ListTaskRunsByParent(_ context.Context, workflowRunID string, parentRunID string) ([]*store.TaskRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*store.TaskRun
	for _, r := range s.taskRuns {
		if r.WorkflowRunID == workflowRunID && r.ParentRunID == parentRunID {
			out = append(out, cloneTaskRun(r))
		}
	}
	return out, nil
}

func (s *MemoryStore) ListActiveTaskRuns(_ context.Context) ([]*store.TaskRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*store.TaskRun
	for _, r := range s.taskRuns {
		if r.Deadline == nil {
			continue
		}
		if r.Status != nil && (*r.Status).IsTerminal() {
			continue
		}
		out = append(out, cloneTaskRun(r))
	}
	return out, nil
}

// --- SchemaStore ---

func (s *MemoryStore) UpsertSchema(_ context.Context, workerID string, schema model.ExecutorSchema) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.schemas[schema.Type+"/"+workerID] = store.SchemaRecord{WorkerID: workerID, Schema: schema}
	return nil
}

func (s *MemoryStore) ListSchemas(_ context.Context) ([]store.SchemaRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]store.SchemaRecord, 0, len(s.schemas))
	for _, r := range s.schemas {
		out = append(out, r)
	}
	return out, nil
}

func (s *MemoryStore) DeleteSchema(_ context.Context, execType, workerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, r := range s.schemas {
		if execType != "" && r.Schema.Type != execType {
			continue
		}
		if workerID != "" && r.WorkerID != workerID {
			continue
		}
		delete(s.schemas, k)
	}
	return nil
}

// --- CronWorkflowStore (unused in W1; POC has no scheduled workflows) ---

func (s *MemoryStore) CreateCronWorkflow(_ context.Context, record *store.CronWorkflowRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.crons[record.CronID]; exists {
		return fmt.Errorf("cron workflow %s already exists", record.CronID)
	}
	cp := *record
	s.crons[record.CronID] = &cp
	return nil
}

func (s *MemoryStore) GetCronWorkflow(_ context.Context, cronID string) (*store.CronWorkflowRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.crons[cronID]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := *r
	return &cp, nil
}

func (s *MemoryStore) UpdateCronWorkflow(_ context.Context, record *store.CronWorkflowRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.crons[record.CronID]; !ok {
		return store.ErrNotFound
	}
	cp := *record
	s.crons[record.CronID] = &cp
	return nil
}

func (s *MemoryStore) DeleteCronWorkflow(_ context.Context, cronID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.crons[cronID]; !ok {
		return store.ErrNotFound
	}
	delete(s.crons, cronID)
	return nil
}

func (s *MemoryStore) ListCronWorkflows(_ context.Context) ([]*store.CronWorkflowRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*store.CronWorkflowRecord, 0, len(s.crons))
	for _, r := range s.crons {
		cp := *r
		out = append(out, &cp)
	}
	return out, nil
}

func (s *MemoryStore) Close() error { return nil }

var _ store.Store = (*MemoryStore)(nil)
