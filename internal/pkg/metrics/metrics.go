// Package metrics defines this project's Prometheus metrics (one shared
// registry so api and scheduler's /metrics endpoints — see their Router()
// wiring — expose a consistent metric namespace, "aigc_"). Kept in its own
// tiny package rather than scattered ad-hoc prometheus.MustRegister calls,
// same reasoning as internal/pkg/config: one small file per cross-cutting
// concern instead of an abstraction the POC doesn't need yet.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// HTTP request metrics — cmd/api's Gin middleware (see server.go).
var (
	HTTPRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "aigc_http_requests_total",
		Help: "Total HTTP requests handled by cmd/api, by route and status code.",
	}, []string{"method", "path", "status"})

	HTTPRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "aigc_http_request_duration_seconds",
		Help:    "HTTP request latency in seconds, by route.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "path"})
)

// Job/task metrics — incremented from internal/application/projection's
// existing OnTaskRun/OnWorkflowRun hooks, which already see every state
// transition centrally in cmd/scheduler regardless of which cmd/worker
// instance actually executed a task — the natural single point to count
// from rather than duplicating counters into cmd/worker as well.
var (
	TaskRunsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "aigc_task_runs_total",
		Help: "Total Aether task runs reaching a terminal phase, by executor type and phase.",
	}, []string{"executor_type", "phase"})

	JobsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "aigc_jobs_total",
		Help: "Total jobs reaching a terminal status, by workflow name and status.",
	}, []string{"workflow_name", "status"})

	CreditsCommittedYuan = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "aigc_credits_committed_yuan_total",
		Help: "Total real MiniMax spend committed via credit settlement, by executor type.",
	}, []string{"executor_type"})
)
