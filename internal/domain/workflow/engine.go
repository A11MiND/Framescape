// Package workflow defines execution plans and the snapshot types business
// code reads. Only internal/infra/orchestrator executes plans.
package workflow

import "time"

// NodeState is one node of a job as exposed to callers.
type NodeState struct {
	Name        string
	Executor    string
	Status      string
	Phase       string // legacy phase vocabulary kept for API compatibility
	LoopIndex   int
	Attempt     int
	QueueReason string
	Outputs     map[string]any
	ErrorCode   string
	ErrorMsg    string
	CreditCost  int
	StartedAt   *time.Time
	FinishedAt  *time.Time
	Display     map[string]any
}

// Run is a snapshot of one job's nodes.
type Run struct {
	Phase string
	Nodes []NodeState
}
