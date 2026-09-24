// Package providergw records every paid outbound provider call in
// provider_calls: which user, job, node and attempt it served, how long it
// took and how it ended. Node costs are added when the node settles. The
// table is the basis for real-currency spend reporting and for spotting a
// paid call that was issued twice.
package providergw

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"strings"
	"time"

	"aigc-platform/internal/infra/executor/spi/executor"
)

// Recorder writes provider_calls rows.
type Recorder struct {
	db *sql.DB
}

func NewRecorder(db *sql.DB) *Recorder { return &Recorder{db: db} }

// Call describes one call recorded outside a transport, for callers that
// know the cost themselves (planning calls made by the api).
type Call struct {
	Provider  string
	Model     string
	Operation string
	UserID    uint64
	JobID     uint64
	Status    string
	Latency   time.Duration
	CostYuan  float64
	ErrorCode string
}

// Record inserts a finished call.
func (r *Recorder) Record(ctx context.Context, c Call) {
	if r == nil || r.db == nil {
		return
	}
	now := time.Now().UTC()
	_, _ = r.db.ExecContext(ctx, `INSERT INTO provider_calls (provider, model, operation, user_id, job_id, status, error_code, latency_ms, cost_yuan, started_at, finished_at)
		VALUES (?, ?, ?, NULLIF(?, 0), NULLIF(?, 0), ?, ?, ?, ?, ?, ?)`,
		c.Provider, c.Model, c.Operation, c.UserID, c.JobID, c.Status, c.ErrorCode, c.Latency.Milliseconds(), c.CostYuan, now.Add(-c.Latency), now)
}

// Transport wraps base so every mutating request (POST) to the provider is
// recorded. Reads (task status polls, result downloads) are not billed and
// are skipped to keep the table small.
func (r *Recorder) Transport(provider string, base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &transport{r: r, provider: provider, base: base}
}

type transport struct {
	r        *Recorder
	provider string
	base     http.RoundTripper
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodPost || t.r == nil || t.r.db == nil {
		return t.base.RoundTrip(req)
	}
	a, _ := executor.AttributionFrom(req.Context())
	op := operation(req.URL.Path)
	started := time.Now().UTC()
	// Recorded before the request goes out: a crash mid-call leaves a
	// 'started' row that shows a paid call may have happened.
	res, err := t.r.db.ExecContext(context.WithoutCancel(req.Context()), `INSERT INTO provider_calls
		(provider, operation, user_id, job_id, node_id, attempt, status, started_at)
		VALUES (?, ?, NULLIF(?, 0), NULLIF(?, 0), NULLIF(?, 0), ?, 'started', ?)`,
		t.provider, op, a.UserID, a.JobID, a.NodeID, a.Attempt, started)
	var id int64
	if err == nil {
		id, _ = res.LastInsertId()
	}
	resp, rtErr := t.base.RoundTrip(req)
	status, httpStatus := "succeeded", 0
	switch {
	case rtErr != nil:
		status = "unknown"
	case resp.StatusCode >= 400:
		status, httpStatus = "failed", resp.StatusCode
	default:
		httpStatus = resp.StatusCode
	}
	if id > 0 {
		finish := func() {
			_, _ = t.r.db.ExecContext(context.Background(), `UPDATE provider_calls SET status = ?, http_status = ?, latency_ms = ?, finished_at = ? WHERE id = ?`,
				status, httpStatus, time.Since(started).Milliseconds(), time.Now().UTC(), id)
		}
		if resp != nil && resp.Body != nil {
			resp.Body = &onClose{ReadCloser: resp.Body, fn: finish}
		} else {
			finish()
		}
	}
	return resp, rtErr
}

// operation is the last two path segments, e.g. "v1/image_generation" or
// "images/edits".
func operation(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) > 2 {
		parts = parts[len(parts)-2:]
	}
	return strings.Join(parts, "/")
}

// onClose defers recording until the body is consumed, so latency covers the
// whole response (image payloads are large).
type onClose struct {
	io.ReadCloser
	fn   func()
	done bool
}

func (o *onClose) Close() error {
	err := o.ReadCloser.Close()
	if !o.done {
		o.done = true
		o.fn()
	}
	return err
}
