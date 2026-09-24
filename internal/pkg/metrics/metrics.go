// Package metrics defines the Prometheus metrics exposed by api and worker
// under the "aigc_" namespace.
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

// Job and node metrics, counted by the orchestrator hooks in the process that
// finished the node (a worker, or the api for cancellations).
var (
	TaskRunsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "aigc_task_runs_total",
		Help: "Total node executions reaching a terminal status, by executor type and phase.",
	}, []string{"executor_type", "phase"})

	JobsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "aigc_jobs_total",
		Help: "Total jobs reaching a terminal status, by workflow name and status.",
	}, []string{"workflow_name", "status"})

	CreditsCommittedYuan = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "aigc_credits_committed_yuan_total",
		Help: "Total real provider spend (CNY) settled, by executor type.",
	}, []string{"executor_type"})
)
