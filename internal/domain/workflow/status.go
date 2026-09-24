package workflow

// Node statuses of the v2 orchestrator.
const (
	NodePending   = "pending"   // waiting for dependencies
	NodeReady     = "ready"     // dispatchable (possibly deferred via next_run_at)
	NodeRunning   = "running"   // leased by a worker
	NodeWaiting   = "waiting"   // an external provider task is in flight; no worker holds it
	NodeSuspended = "suspended" // a gate waiting for a user decision
	NodeSucceeded = "succeeded"
	NodeFailed    = "failed"
	NodeCancelled = "cancelled"
	NodeSkipped   = "skipped" // an upstream node failed or was cancelled
)

// Job statuses written by the v2 orchestrator.
const (
	JobQueued         = "queued"
	JobRunning        = "running"
	JobAwaitingReview = "awaiting_review"
	JobSucceeded      = "succeeded"
	JobPartial        = "partial"
	JobFailed         = "failed"
	JobCancelling     = "cancelling"
	JobCancelled      = "cancelled"
)

// NodeTerminal reports whether a node status is final.
func NodeTerminal(status string) bool {
	switch status {
	case NodeSucceeded, NodeFailed, NodeCancelled, NodeSkipped:
		return true
	}
	return false
}

// JobTerminal reports whether a job status is final.
func JobTerminal(status string) bool {
	switch status {
	case JobSucceeded, JobPartial, JobFailed, JobCancelled:
		return true
	}
	return false
}

// LegacyPhase maps a v2 node status to the phase vocabulary the API and
// frontend used with the previous engine, so existing clients keep working.
func LegacyPhase(status string) string {
	switch status {
	case NodePending:
		return "Created"
	case NodeReady:
		return "Ready"
	case NodeRunning, NodeWaiting:
		return "Running"
	case NodeSuspended:
		return "Suspended"
	case NodeSucceeded:
		return "Succeeded"
	case NodeFailed:
		return "Failed"
	case NodeCancelled:
		return "Cancelled"
	case NodeSkipped:
		return "Skipped"
	}
	return ""
}
