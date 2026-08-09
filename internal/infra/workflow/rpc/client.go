package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"aigc-platform/internal/domain/workflow"
)

// Client implements workflow.Engine by calling a remote Server (cmd/scheduler)
// over HTTP. This is what cmd/api wires as its workflow.Engine — it never
// holds an Aether instance itself (PRD §8.1).
type Client struct {
	baseURL string
	http    *http.Client
}

func NewClient(baseURL string) *Client {
	return &Client{baseURL: baseURL, http: &http.Client{Timeout: 15 * time.Second}}
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var reader *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("scheduler request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		var e ErrorResponse
		_ = json.NewDecoder(resp.Body).Decode(&e)
		if e.Error == "" {
			e.Error = fmt.Sprintf("scheduler returned status %d", resp.StatusCode)
		}
		return fmt.Errorf("%s", e.Error)
	}
	if out != nil && resp.StatusCode != http.StatusNoContent {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

func (c *Client) Submit(ctx context.Context, def *workflow.Definition, args map[string]any) (workflow.RunID, error) {
	var resp SubmitResponse
	err := c.do(ctx, http.MethodPost, "/internal/engine/runs", SubmitRequest{
		DefinitionName: def.Name,
		WorkflowJSON:   def.JSON,
		Args:           args,
	}, &resp)
	if err != nil {
		return "", err
	}
	return workflow.RunID(resp.RunID), nil
}

func (c *Client) Get(ctx context.Context, id workflow.RunID) (*workflow.Run, error) {
	var resp GetResponse
	if err := c.do(ctx, http.MethodGet, "/internal/engine/runs/"+string(id), nil, &resp); err != nil {
		return nil, err
	}
	run := &workflow.Run{ID: workflow.RunID(resp.ID), Phase: resp.Phase}
	for _, n := range resp.Nodes {
		run.Nodes = append(run.Nodes, workflow.NodeState{
			TaskRunID:    n.TaskRunID,
			Name:         n.Name,
			LoopIndex:    n.LoopIndex,
			ParentScope:  n.ParentScope,
			ExecutorType: n.ExecutorType,
			Phase:        n.Phase,
			ExecCode:     n.ExecCode,
			Outputs:      n.Outputs,
			ErrorMsg:     n.ErrorMsg,
		})
	}
	return run, nil
}

func (c *Client) Cancel(ctx context.Context, id workflow.RunID) error {
	return c.do(ctx, http.MethodPost, "/internal/engine/runs/"+string(id)+"/cancel", nil, nil)
}

func (c *Client) Resume(ctx context.Context, id workflow.RunID, taskName string, inputs map[string]any) error {
	return c.do(ctx, http.MethodPost, "/internal/engine/runs/"+string(id)+"/resume", ResumeRequest{
		TaskName: taskName, Inputs: inputs,
	}, nil)
}

var _ workflow.Engine = (*Client)(nil)
