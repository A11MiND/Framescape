package aetherengine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/BabySid/aether/model"
	"github.com/BabySid/aether/store"
)

// ChangeCallback is invoked by MySQLStore after every successful write, and
// is the ONLY reliable per-change hook in this system — see
// docs/aether-validation-report.md §六: hook.Notifier is opt-in/declarative
// and does not fire for plain phase transitions, so job_nodes projection and
// SSE (internal/application/projection, DEV_PLAN.md §6) are wired here
// instead, not through hook.Notifier.
type ChangeCallback struct {
	OnTaskRun     func(ctx context.Context, tr *store.TaskRun)
	OnWorkflowRun func(ctx context.Context, wr *store.WorkflowRun)
}

// MySQLStore implements store.Store against aether_workflow_runs /
// aether_task_runs (migrations/00002_aether_store.sql). State-transition
// writes use hand-written parameterised SQL with an explicit token (CAS)
// check and RowsAffected verification — no GORM AutoMigrate/Hooks (PRD
// §15.1/R11). CronWorkflowStore/SchemaStore are kept in-memory: the POC has
// no scheduled workflows and no distributed worker schema announcement
// (executors are registered directly at process construction), so those two
// sub-interfaces exist only to satisfy store.Store's Go signature.
type MySQLStore struct {
	db *sql.DB
	cb ChangeCallback

	cronMu  sync.Mutex
	crons   map[string]*store.CronWorkflowRecord
	schMu   sync.Mutex
	schemas map[string]store.SchemaRecord
}

func NewMySQLStore(db *sql.DB) *MySQLStore {
	return &MySQLStore{
		db:      db,
		crons:   make(map[string]*store.CronWorkflowRecord),
		schemas: make(map[string]store.SchemaRecord),
	}
}

// SetChangeCallback wires the projection hook. Called once at startup,
// before the store is handed to aetherengine.New.
func (s *MySQLStore) SetChangeCallback(cb ChangeCallback) { s.cb = cb }

// --- WorkflowRunStore ---

func (s *MySQLStore) CreateWorkflowRun(ctx context.Context, run *store.WorkflowRun) error {
	wfJSON := run.Workflow
	if len(wfJSON) == 0 {
		wfJSON = []byte("{}")
	}
	// The engine sets Status=PhaseCreated before calling this (engine_helper.go's
	// submitInternal) and every later transition (Ready/Running/terminal) is
	// guarded on the CURRENT status being an exact expected value
	// (markAncestorsReady/markAncestorsRunning/finalizeWorkflow) — hardcoding
	// this to '' instead of run.Status silently wedges the whole state
	// machine at the workflow level (caught by testing the real distributed
	// W2 stack: tasks completed fine, but aether_workflow_runs.status never
	// left '', so finalizeWorkflow's guard always no-opped).
	status := ""
	if run.Status != nil {
		status = string(*run.Status)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO aether_workflow_runs (run_id, workflow_json, cron_workflow_id, status, token)
		VALUES (?, ?, ?, ?, 1)
		ON DUPLICATE KEY UPDATE run_id = run_id`, // idempotent create, per store.WorkflowRunStore contract
		run.RunID, []byte(wfJSON), run.CronWorkflowID, status)
	if err != nil {
		return fmt.Errorf("insert aether_workflow_runs: %w", err)
	}
	if s.cb.OnWorkflowRun != nil {
		if wr, err := s.GetWorkflowRun(ctx, run.RunID); err == nil {
			s.cb.OnWorkflowRun(ctx, wr)
		}
	}
	return nil
}

func (s *MySQLStore) GetWorkflowRun(ctx context.Context, runID string) (*store.WorkflowRun, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT run_id, workflow_json, cron_workflow_id, status, message, outputs_json, metrics_json,
		       deadline, token, created_at, updated_at
		FROM aether_workflow_runs WHERE run_id = ?`, runID)
	return scanWorkflowRun(row)
}

func (s *MySQLStore) UpdateWorkflowRun(ctx context.Context, run *store.WorkflowRun) (*store.WorkflowRun, error) {
	sets := []string{"token = token + 1"}
	args := []any{}
	if run.Status != nil {
		sets = append(sets, "status = ?")
		args = append(args, string(*run.Status))
	}
	if run.Message != nil {
		sets = append(sets, "message = ?")
		args = append(args, *run.Message)
	}
	if run.Outputs != nil {
		b, err := json.Marshal(run.Outputs)
		if err != nil {
			return nil, fmt.Errorf("marshal outputs: %w", err)
		}
		sets = append(sets, "outputs_json = ?")
		args = append(args, b)
	}
	if run.Metrics != nil {
		b, err := json.Marshal(run.Metrics)
		if err != nil {
			return nil, fmt.Errorf("marshal metrics: %w", err)
		}
		sets = append(sets, "metrics_json = ?")
		args = append(args, b)
	}
	if run.Deadline != nil {
		sets = append(sets, "deadline = ?")
		args = append(args, run.Deadline.UTC())
	}

	query := "UPDATE aether_workflow_runs SET " + joinComma(sets) + " WHERE run_id = ?"
	args = append(args, run.RunID)
	if run.Token != 0 {
		query += " AND token = ?"
		args = append(args, run.Token)
	}

	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("update aether_workflow_runs: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		if run.Token != 0 {
			return nil, fmt.Errorf("workflow run %s: %w", run.RunID, store.ErrTokenMismatch)
		}
		return nil, store.ErrNotFound
	}

	updated, err := s.GetWorkflowRun(ctx, run.RunID)
	if err != nil {
		return nil, err
	}
	if s.cb.OnWorkflowRun != nil {
		s.cb.OnWorkflowRun(ctx, updated)
	}
	return updated, nil
}

func (s *MySQLStore) ListActiveWorkflowRuns(ctx context.Context) ([]*store.WorkflowRun, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT run_id, workflow_json, cron_workflow_id, status, message, outputs_json, metrics_json,
		       deadline, token, created_at, updated_at
		FROM aether_workflow_runs
		WHERE deadline IS NOT NULL AND status NOT IN ('Succeeded','Failed','Error','Timeout','Skipped','Cancelled')`)
	if err != nil {
		return nil, fmt.Errorf("list active workflow runs: %w", err)
	}
	defer rows.Close()
	var out []*store.WorkflowRun
	for rows.Next() {
		wr, err := scanWorkflowRunRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, wr)
	}
	return out, rows.Err()
}

func (s *MySQLStore) DeleteWorkflowRun(ctx context.Context, runID string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM aether_workflow_runs WHERE run_id = ?`, runID)
	if err != nil {
		return fmt.Errorf("delete workflow run: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return store.ErrNotFound
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM aether_task_runs WHERE workflow_run_id = ?`, runID); err != nil {
		return fmt.Errorf("cascade delete task runs: %w", err)
	}
	return nil
}

func (s *MySQLStore) ListWorkflowRunsByCronID(ctx context.Context, cronWorkflowID string) ([]*store.WorkflowRun, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT run_id, workflow_json, cron_workflow_id, status, message, outputs_json, metrics_json,
		       deadline, token, created_at, updated_at
		FROM aether_workflow_runs WHERE cron_workflow_id = ?`, cronWorkflowID)
	if err != nil {
		return nil, fmt.Errorf("list workflow runs by cron id: %w", err)
	}
	defer rows.Close()
	var out []*store.WorkflowRun
	for rows.Next() {
		wr, err := scanWorkflowRunRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, wr)
	}
	return out, rows.Err()
}

// --- TaskRunStore ---

func (s *MySQLStore) CreateTaskRun(ctx context.Context, run *store.TaskRun) error {
	var inputsJSON []byte
	if run.Inputs != nil {
		b, err := json.Marshal(run.Inputs)
		if err != nil {
			return fmt.Errorf("marshal inputs: %w", err)
		}
		inputsJSON = b
	}
	status := ""
	if run.Status != nil {
		status = string(*run.Status)
	}

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO aether_task_runs
			(run_id, workflow_run_id, parent_run_id, depth, scope, task_name, template_name, template_type,
			 inputs_json, status, token)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1)
		ON DUPLICATE KEY UPDATE run_id = run_id`, // idempotent by (workflow_run_id, parent_run_id, scope, task_name)
		run.RunID, run.WorkflowRunID, run.ParentRunID, run.Depth, run.Scope, run.TaskName,
		run.TemplateName, run.TemplateType, inputsJSON, status)
	if err != nil {
		return fmt.Errorf("insert aether_task_runs: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil // duplicate key hit; treated as no-op per documented contract
	}
	if s.cb.OnTaskRun != nil {
		if tr, err := s.GetTaskRun(ctx, run.RunID); err == nil {
			s.cb.OnTaskRun(ctx, tr)
		}
	}
	return nil
}

func (s *MySQLStore) GetTaskRun(ctx context.Context, taskRunID string) (*store.TaskRun, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT run_id, workflow_run_id, parent_run_id, depth, scope, task_name, template_name, template_type,
		       inputs_json, status, message, outputs_json, metrics_json, retry_count, deadline, token,
		       created_at, updated_at
		FROM aether_task_runs WHERE run_id = ?`, taskRunID)
	return scanTaskRun(row)
}

func (s *MySQLStore) UpdateTaskRun(ctx context.Context, run *store.TaskRun) (*store.TaskRun, error) {
	sets := []string{"token = token + 1"}
	args := []any{}
	if run.Inputs != nil {
		b, err := json.Marshal(run.Inputs)
		if err != nil {
			return nil, fmt.Errorf("marshal inputs: %w", err)
		}
		sets = append(sets, "inputs_json = ?")
		args = append(args, b)
	}
	if run.Status != nil {
		sets = append(sets, "status = ?")
		args = append(args, string(*run.Status))
	}
	if run.Message != nil {
		sets = append(sets, "message = ?")
		args = append(args, *run.Message)
	}
	if run.Outputs != nil {
		b, err := json.Marshal(run.Outputs)
		if err != nil {
			return nil, fmt.Errorf("marshal outputs: %w", err)
		}
		sets = append(sets, "outputs_json = ?")
		args = append(args, b)
	}
	if run.Metrics != nil {
		b, err := json.Marshal(run.Metrics)
		if err != nil {
			return nil, fmt.Errorf("marshal metrics: %w", err)
		}
		sets = append(sets, "metrics_json = ?")
		args = append(args, b)
	}
	if run.RetryCount != nil {
		sets = append(sets, "retry_count = ?")
		args = append(args, *run.RetryCount)
	}
	if run.Deadline != nil {
		sets = append(sets, "deadline = ?")
		args = append(args, run.Deadline.UTC())
	}

	query := "UPDATE aether_task_runs SET " + joinComma(sets) + " WHERE run_id = ?"
	args = append(args, run.RunID)
	if run.Token != 0 {
		query += " AND token = ?"
		args = append(args, run.Token)
	}

	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("update aether_task_runs: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		if run.Token != 0 {
			return nil, fmt.Errorf("task run %s: %w", run.RunID, store.ErrTokenMismatch)
		}
		return nil, store.ErrNotFound
	}

	updated, err := s.GetTaskRun(ctx, run.RunID)
	if err != nil {
		return nil, err
	}
	if s.cb.OnTaskRun != nil {
		s.cb.OnTaskRun(ctx, updated)
	}
	return updated, nil
}

func (s *MySQLStore) ListTaskRuns(ctx context.Context, workflowRunID string) ([]*store.TaskRun, error) {
	return s.queryTaskRuns(ctx, `WHERE workflow_run_id = ?`, workflowRunID)
}

func (s *MySQLStore) ListTaskRunsByParent(ctx context.Context, workflowRunID string, parentRunID string) ([]*store.TaskRun, error) {
	return s.queryTaskRuns(ctx, `WHERE workflow_run_id = ? AND parent_run_id = ?`, workflowRunID, parentRunID)
}

func (s *MySQLStore) ListActiveTaskRuns(ctx context.Context) ([]*store.TaskRun, error) {
	return s.queryTaskRuns(ctx,
		`WHERE deadline IS NOT NULL AND status NOT IN ('Succeeded','Failed','Error','Timeout','Skipped','Cancelled')`)
}

func (s *MySQLStore) queryTaskRuns(ctx context.Context, where string, args ...any) ([]*store.TaskRun, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT run_id, workflow_run_id, parent_run_id, depth, scope, task_name, template_name, template_type,
		       inputs_json, status, message, outputs_json, metrics_json, retry_count, deadline, token,
		       created_at, updated_at
		FROM aether_task_runs `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("query task runs: %w", err)
	}
	defer rows.Close()
	var out []*store.TaskRun
	for rows.Next() {
		tr, err := scanTaskRunRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, tr)
	}
	return out, rows.Err()
}

// --- SchemaStore (in-memory; see type doc) ---

func (s *MySQLStore) UpsertSchema(_ context.Context, workerID string, schema model.ExecutorSchema) error {
	s.schMu.Lock()
	defer s.schMu.Unlock()
	s.schemas[schema.Type+"/"+workerID] = store.SchemaRecord{WorkerID: workerID, Schema: schema}
	return nil
}

func (s *MySQLStore) ListSchemas(_ context.Context) ([]store.SchemaRecord, error) {
	s.schMu.Lock()
	defer s.schMu.Unlock()
	out := make([]store.SchemaRecord, 0, len(s.schemas))
	for _, r := range s.schemas {
		out = append(out, r)
	}
	return out, nil
}

func (s *MySQLStore) DeleteSchema(_ context.Context, execType, workerID string) error {
	s.schMu.Lock()
	defer s.schMu.Unlock()
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

// --- CronWorkflowStore (in-memory; see type doc) ---

func (s *MySQLStore) CreateCronWorkflow(_ context.Context, record *store.CronWorkflowRecord) error {
	s.cronMu.Lock()
	defer s.cronMu.Unlock()
	if _, exists := s.crons[record.CronID]; exists {
		return fmt.Errorf("cron workflow %s already exists", record.CronID)
	}
	cp := *record
	s.crons[record.CronID] = &cp
	return nil
}

func (s *MySQLStore) GetCronWorkflow(_ context.Context, cronID string) (*store.CronWorkflowRecord, error) {
	s.cronMu.Lock()
	defer s.cronMu.Unlock()
	r, ok := s.crons[cronID]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := *r
	return &cp, nil
}

func (s *MySQLStore) UpdateCronWorkflow(_ context.Context, record *store.CronWorkflowRecord) error {
	s.cronMu.Lock()
	defer s.cronMu.Unlock()
	if _, ok := s.crons[record.CronID]; !ok {
		return store.ErrNotFound
	}
	cp := *record
	s.crons[record.CronID] = &cp
	return nil
}

func (s *MySQLStore) DeleteCronWorkflow(_ context.Context, cronID string) error {
	s.cronMu.Lock()
	defer s.cronMu.Unlock()
	if _, ok := s.crons[cronID]; !ok {
		return store.ErrNotFound
	}
	delete(s.crons, cronID)
	return nil
}

func (s *MySQLStore) ListCronWorkflows(_ context.Context) ([]*store.CronWorkflowRecord, error) {
	s.cronMu.Lock()
	defer s.cronMu.Unlock()
	out := make([]*store.CronWorkflowRecord, 0, len(s.crons))
	for _, r := range s.crons {
		cp := *r
		out = append(out, &cp)
	}
	return out, nil
}

func (s *MySQLStore) Close() error { return s.db.Close() }

var _ store.Store = (*MySQLStore)(nil)

// --- scan helpers ---

type rowScanner interface {
	Scan(dest ...any) error
}

func scanWorkflowRun(row rowScanner) (*store.WorkflowRun, error) {
	var (
		runID, cronID, status, message string
		wfJSON                         []byte
		outputsJSON, metricsJSON       sql.NullString
		deadline                       sql.NullTime
		token                          uint64
		createdAt, updatedAt           time.Time
	)
	err := row.Scan(&runID, &wfJSON, &cronID, &status, &message, &outputsJSON, &metricsJSON,
		&deadline, &token, &createdAt, &updatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("scan workflow run: %w", err)
	}
	wr := &store.WorkflowRun{
		RunID: runID, Workflow: json.RawMessage(wfJSON), CronWorkflowID: cronID,
		CreatedAt: createdAt, UpdatedAt: updatedAt, Token: token,
	}
	if status != "" {
		p := model.Phase(status)
		wr.Status = &p
	}
	if message != "" {
		wr.Message = &message
	}
	if outputsJSON.Valid {
		var o model.Outputs
		if err := json.Unmarshal([]byte(outputsJSON.String), &o); err == nil {
			wr.Outputs = &o
		}
	}
	if metricsJSON.Valid {
		var m model.Metrics
		if err := json.Unmarshal([]byte(metricsJSON.String), &m); err == nil {
			wr.Metrics = &m
		}
	}
	if deadline.Valid {
		wr.Deadline = &deadline.Time
	}
	return wr, nil
}

func scanWorkflowRunRows(rows *sql.Rows) (*store.WorkflowRun, error) { return scanWorkflowRun(rows) }

func scanTaskRun(row rowScanner) (*store.TaskRun, error) {
	var (
		runID, wfRunID, parentRunID, scope, taskName, templateName, templateType, status, message string
		inputsJSON, outputsJSON, metricsJSON                                                      sql.NullString
		depth                                                                                     int
		retryCount                                                                                sql.NullInt64
		deadline                                                                                  sql.NullTime
		token                                                                                     uint64
		createdAt, updatedAt                                                                      time.Time
	)
	err := row.Scan(&runID, &wfRunID, &parentRunID, &depth, &scope, &taskName, &templateName, &templateType,
		&inputsJSON, &status, &message, &outputsJSON, &metricsJSON, &retryCount, &deadline, &token,
		&createdAt, &updatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("scan task run: %w", err)
	}
	tr := &store.TaskRun{
		RunID: runID, WorkflowRunID: wfRunID, ParentRunID: parentRunID, Depth: depth, Scope: scope,
		TaskName: taskName, TemplateName: templateName, TemplateType: templateType,
		CreatedAt: createdAt, UpdatedAt: updatedAt, Token: token,
	}
	if status != "" {
		p := model.Phase(status)
		tr.Status = &p
	}
	if message != "" {
		tr.Message = &message
	}
	if inputsJSON.Valid {
		var in model.Inputs
		if err := json.Unmarshal([]byte(inputsJSON.String), &in); err == nil {
			tr.Inputs = &in
		}
	}
	if outputsJSON.Valid {
		var o model.Outputs
		if err := json.Unmarshal([]byte(outputsJSON.String), &o); err == nil {
			tr.Outputs = &o
		}
	}
	if metricsJSON.Valid {
		var m model.Metrics
		if err := json.Unmarshal([]byte(metricsJSON.String), &m); err == nil {
			tr.Metrics = &m
		}
	}
	if retryCount.Valid {
		v := int(retryCount.Int64)
		tr.RetryCount = &v
	}
	if deadline.Valid {
		tr.Deadline = &deadline.Time
	}
	return tr, nil
}

func scanTaskRunRows(rows *sql.Rows) (*store.TaskRun, error) { return scanTaskRun(rows) }

func joinComma(parts []string) string {
	out := parts[0]
	for _, p := range parts[1:] {
		out += ", " + p
	}
	return out
}
