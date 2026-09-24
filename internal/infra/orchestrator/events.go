package orchestrator

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Event types written to job_events.
const (
	EventJobCreated   = "job.created"
	EventJobStatus    = "job.status"
	EventNodeStatus   = "node.status"
	EventNeedsReview  = "job.needs_review"
	EventJobFinished  = "job.finished"
	EventCreditChange = "credits.changed"
)

// Event is one row of job_events as delivered to clients.
type Event struct {
	ID        uint64          `json:"id"`
	UserID    uint64          `json:"-"`
	JobBizID  string          `json:"job_id"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
}

// eventBuffer collects events inside a transaction; they are published once
// the transaction commits.
type eventBuffer struct {
	job    JobRef
	events []Event
}

func (b *eventBuffer) add(ctx context.Context, tx *sql.Tx, now time.Time, typ string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal %s event: %w", typ, err)
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO job_events (user_id, job_id, job_biz_id, type, payload, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		b.job.UserID, b.job.ID, b.job.BizID, typ, raw, now)
	if err != nil {
		return fmt.Errorf("insert %s event: %w", typ, err)
	}
	eventID, _ := res.LastInsertId()
	b.events = append(b.events, Event{ID: uint64(eventID), UserID: b.job.UserID, JobBizID: b.job.BizID, Type: typ, Payload: raw, CreatedAt: now})
	return nil
}

// publish sends buffered events and marks them published. Failures are left
// for the sweeper, which re-publishes unmarked rows.
func (o *Orchestrator) publish(ctx context.Context, events []Event) {
	if len(events) == 0 || o.pub == nil {
		return
	}
	if err := o.pub.PublishEvents(ctx, events); err != nil {
		return
	}
	ids := make([]any, len(events))
	marks := make([]string, len(events))
	for i, e := range events {
		ids[i] = e.ID
		marks[i] = "?"
	}
	args := append([]any{o.now()}, ids...)
	_, _ = o.db.ExecContext(ctx, `UPDATE job_events SET published_at = ? WHERE id IN (`+strings.Join(marks, ",")+`)`, args...)
}

// EventsSince returns up to limit events for a user after the given id, for
// SSE resumption with Last-Event-ID.
func (o *Orchestrator) EventsSince(ctx context.Context, userID, afterID uint64, limit int) ([]Event, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	rows, err := o.db.QueryContext(ctx, `SELECT id, job_biz_id, type, payload, created_at FROM job_events
		WHERE user_id = ? AND id > ? ORDER BY id LIMIT ?`, userID, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("query events: %w", err)
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		e := Event{UserID: userID}
		if err := rows.Scan(&e.ID, &e.JobBizID, &e.Type, &e.Payload, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// LatestEventID is where a fresh SSE subscriber without Last-Event-ID starts.
func (o *Orchestrator) LatestEventID(ctx context.Context, userID uint64) (uint64, error) {
	var id sql.NullInt64
	err := o.db.QueryRowContext(ctx, `SELECT MAX(id) FROM job_events WHERE user_id = ?`, userID).Scan(&id)
	return uint64(id.Int64), err
}
