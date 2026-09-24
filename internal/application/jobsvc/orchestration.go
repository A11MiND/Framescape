package jobsvc

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"aigc-platform/internal/application/creditsvc"
	"aigc-platform/internal/domain/workflow"
	"aigc-platform/internal/infra/orchestrator"
	"aigc-platform/internal/pkg/metrics"
)

// TaskAssetReview is the follow-up task reviewing a generated image.
const TaskAssetReview = "asset:review"

// AssetReview is its payload.
type AssetReview struct {
	JobID     uint64 `json:"job_id"`
	UserID    uint64 `json:"user_id"`
	TaskRunID string `json:"task_run_id"`
	AssetID   string `json:"asset_id"`
	Executor  string `json:"executor"`
}

// Billing settles node costs against the job's own reservation.
type Billing struct{ Credits *creditsvc.Service }

func hold(job orchestrator.JobRef) creditsvc.JobHold {
	return creditsvc.JobHold{UserID: job.UserID, JobID: job.ID, JobBizID: job.BizID}
}

func (b Billing) CommitTx(ctx context.Context, tx *sql.Tx, job orchestrator.JobRef, node string, attempt int, costYuan float64) (int, error) {
	key := fmt.Sprintf("job:%s:node:%s:a%d:commit", job.BizID, node, attempt)
	return b.Credits.CommitForJobTx(ctx, tx, hold(job), key, node, costYuan)
}

func (b Billing) ReleaseTx(ctx context.Context, tx *sql.Tx, job orchestrator.JobRef) (int, error) {
	return b.Credits.ReleaseJobTx(ctx, tx, hold(job), "job:"+job.BizID+":release")
}

// Hooks records moderation outcomes, schedules post-generation review of
// images and keeps metrics.
type Hooks struct {
	// ReviewImages enables the post-generation review follow-up.
	ReviewImages bool
}

const moderationPrefix = "sensitive_content:"

func (h Hooks) NodeFinishedTx(ctx context.Context, tx *sql.Tx, job orchestrator.JobRef, n orchestrator.FinishedNode) ([]orchestrator.Task, error) {
	metrics.TaskRunsTotal.WithLabelValues(n.Executor, workflow.LegacyPhase(n.Status)).Inc()
	if cost, ok := n.Outputs["cost-yuan"].(float64); ok && cost > 0 {
		metrics.CreditsCommittedYuan.WithLabelValues(n.Executor).Add(cost)
	}
	if n.Status == workflow.NodeFailed && strings.HasPrefix(n.Message, moderationPrefix) {
		if _, err := tx.ExecContext(ctx, `INSERT INTO moderation_records (task_run_id, job_id, user_id, executor_type, provider_message)
			VALUES (?, ?, ?, ?, ?) ON DUPLICATE KEY UPDATE task_run_id = task_run_id`,
			n.TaskRunID, job.ID, job.UserID, n.Executor, truncate(n.Message, 512)); err != nil {
			return nil, fmt.Errorf("record moderation: %w", err)
		}
	}
	if !h.ReviewImages || n.Status != workflow.NodeSucceeded || n.Executor != "minimax.image" {
		return nil, nil
	}
	assetID, _ := n.Outputs["asset-id"].(string)
	if assetID == "" {
		return nil, nil
	}
	payload, _ := json.Marshal(AssetReview{JobID: job.ID, UserID: job.UserID, TaskRunID: n.TaskRunID, AssetID: assetID, Executor: n.Executor})
	return []orchestrator.Task{{Kind: TaskAssetReview, Queue: orchestrator.QueueSystem, Payload: payload, UniqueID: "review:" + assetID}}, nil
}

func (h Hooks) JobFinishedTx(ctx context.Context, tx *sql.Tx, job orchestrator.JobRef, status string) error {
	var name string
	if err := tx.QueryRowContext(ctx, `SELECT workflow_name FROM jobs WHERE id = ?`, job.ID).Scan(&name); err == nil {
		metrics.JobsTotal.WithLabelValues(name, status).Inc()
	}
	return nil
}
