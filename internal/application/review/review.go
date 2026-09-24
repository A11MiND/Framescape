// Package review runs the post-generation content check on images. The
// provider's own filter only covers its policy; this second look flags real
// identifiable people and well-known copyrighted characters. It is advisory:
// it records a moderation entry and never fails the job.
package review

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"aigc-platform/internal/application/jobsvc"
	"aigc-platform/internal/infra/executor/assetstore"
	"aigc-platform/internal/infra/executor/minimax"
)

type Reviewer struct {
	db      *sql.DB
	minimax *minimax.Client
	reader  assetstore.Reader
	http    *http.Client
}

func New(db *sql.DB, client *minimax.Client, reader assetstore.Reader) *Reviewer {
	return &Reviewer{db: db, minimax: client, reader: reader, http: &http.Client{Timeout: 30 * time.Second}}
}

// Handle processes one jobsvc.TaskAssetReview payload.
func (r *Reviewer) Handle(ctx context.Context, payload []byte) error {
	var t jobsvc.AssetReview
	if err := json.Unmarshal(payload, &t); err != nil {
		return nil
	}
	flagged, reason, err := r.review(ctx, t.AssetID)
	if err != nil {
		return err
	}
	if !flagged {
		return nil
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO moderation_records (task_run_id, job_id, user_id, executor_type, provider_message)
		VALUES (?, ?, ?, ?, ?) ON DUPLICATE KEY UPDATE task_run_id = task_run_id`,
		t.TaskRunID, t.JobID, t.UserID, t.Executor, truncate("post_review: "+reason, 512))
	return err
}

// review asks the vision model a strict yes/no question. Only an explicit
// "FLAG" counts; anything else passes, since a false positive costs more
// than a missed advisory flag.
func (r *Reviewer) review(ctx context.Context, assetID string) (bool, string, error) {
	url, err := r.reader.PublicURL(ctx, assetID)
	if err != nil {
		return false, "", fmt.Errorf("look up asset %s: %w", assetID, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, "", err
	}
	resp, err := r.http.Do(req)
	if err != nil {
		return false, "", fmt.Errorf("download asset %s: %w", assetID, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 25<<20))
	if err != nil {
		return false, "", err
	}
	// Our object storage is not reachable from the provider, so the image is
	// sent inline.
	dataURI := "data:" + http.DetectContentType(data) + ";base64," + base64.StdEncoding.EncodeToString(data)
	out, err := r.minimax.ChatCompletion(ctx, minimax.ChatCompletionRequest{
		Model: "MiniMax-M3",
		Messages: []minimax.ChatMessage{{Role: "user", Content: []map[string]any{
			{"type": "image_url", "image_url": map[string]string{"url": dataURI}},
			{"type": "text", "text": "You are a content-safety reviewer for an AI image generation product. Watch for real identifiable people's likenesses and well-known copyrighted characters, alongside standard NSFW/violence concerns. If the image clearly contains any of these, reply with exactly \"FLAG: <one short reason>\" as the first line. Otherwise reply with exactly \"OK\"."},
		}}},
		Temperature: 0, MaxCompletionTokens: 60, Thinking: &minimax.ThinkingConfig{Type: "disabled"},
	})
	if err != nil {
		return false, "", err
	}
	if len(out.Choices) == 0 {
		return false, "", nil
	}
	content := strings.TrimSpace(out.Choices[0].Message.Content)
	if !strings.HasPrefix(content, "FLAG") {
		return false, "", nil
	}
	return true, strings.TrimSpace(strings.TrimPrefix(content, "FLAG:")), nil
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
