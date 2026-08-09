// Package rpc is the wire contract between cmd/api and cmd/scheduler.
//
// Aether's Engine must be a single instance (PRD §8.1: "引擎需要单实例来避免
// 重复推进"), and it lives in cmd/scheduler. cmd/api still needs to submit
// jobs and read their state, so it talks to cmd/scheduler over a small
// internal HTTP API defined here. Both processes program against
// internal/domain/workflow.Engine — cmd/scheduler wires the real
// aetherengine.Engine behind Server, cmd/api wires Client, and nothing else
// in either codebase needs to know the transport exists.
package rpc

// SubmitRequest is POST /internal/engine/runs.
type SubmitRequest struct {
	DefinitionName string         `json:"definition_name"`
	WorkflowJSON   []byte         `json:"workflow_json"`
	Args           map[string]any `json:"args,omitempty"`
}

type SubmitResponse struct {
	RunID string `json:"run_id"`
}

// GetResponse is GET /internal/engine/runs/{run_id}.
type GetResponse struct {
	ID    string          `json:"id"`
	Phase string          `json:"phase"`
	Nodes []NodeStateWire `json:"nodes"`
}

type NodeStateWire struct {
	TaskRunID    string         `json:"task_run_id"`
	Name         string         `json:"name"`
	LoopIndex    int            `json:"loop_index"`
	ParentScope  string         `json:"parent_scope"`
	ExecutorType string         `json:"executor_type"`
	Phase        string         `json:"phase"`
	ExecCode     *int           `json:"exec_code,omitempty"`
	Outputs      map[string]any `json:"outputs,omitempty"`
	ErrorMsg     string         `json:"error_msg,omitempty"`
}

// ResumeRequest is POST /internal/engine/runs/{run_id}/resume.
type ResumeRequest struct {
	TaskName string         `json:"task_name"`
	Inputs   map[string]any `json:"inputs"`
}

// ErrorResponse is returned (with a non-2xx status) on any failure.
type ErrorResponse struct {
	Error string `json:"error"`
}
