package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"aigc-platform/internal/domain/workflow"
	"aigc-platform/internal/infra/executor/spi/model"
)

// resolveInputs turns a node's bindings into executor parameters, reading
// referenced outputs of upstream nodes of the same job.
func resolveInputs(ctx context.Context, q querier, n *Node) (*model.Inputs, error) {
	names := map[string]bool{}
	for _, in := range n.Inputs {
		for _, r := range in.Refs() {
			names[r] = true
		}
	}
	upstream := map[string]map[string]any{}
	if len(names) > 0 {
		args := []any{n.JobID}
		marks := make([]string, 0, len(names))
		for name := range names {
			args = append(args, name)
			marks = append(marks, "?")
		}
		rows, err := q.QueryContext(ctx, `SELECT node_name, status, outputs_json FROM job_nodes WHERE job_id = ? AND node_name IN (`+strings.Join(marks, ",")+`)`, args...)
		if err != nil {
			return nil, fmt.Errorf("load upstream outputs: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var name, status string
			var raw []byte
			if err := rows.Scan(&name, &status, &raw); err != nil {
				return nil, err
			}
			if status != workflow.NodeSucceeded {
				return nil, fmt.Errorf("upstream node %q is %s", name, status)
			}
			out := map[string]any{}
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &out); err != nil {
					return nil, fmt.Errorf("decode outputs of %q: %w", name, err)
				}
			}
			upstream[name] = out
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	keys := make([]string, 0, len(n.Inputs))
	for k := range n.Inputs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	params := make([]model.Parameter, 0, len(keys))
	for _, k := range keys {
		v, err := resolveValue(n.Inputs[k], upstream)
		if err != nil {
			return nil, fmt.Errorf("input %q: %w", k, err)
		}
		raw, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("input %q: %w", k, err)
		}
		params = append(params, model.Parameter{Name: k, Value: raw})
	}
	return &model.Inputs{Parameters: params}, nil
}

func resolveValue(in workflow.Input, upstream map[string]map[string]any) (any, error) {
	switch {
	case in.Ref != nil:
		outs, ok := upstream[in.Ref.Node]
		if !ok {
			return nil, fmt.Errorf("upstream node %q not found", in.Ref.Node)
		}
		v, ok := outs[in.Ref.Output]
		if !ok {
			return nil, fmt.Errorf("node %q has no output %q", in.Ref.Node, in.Ref.Output)
		}
		return v, nil
	case in.IsList || len(in.List) > 0:
		list := []any{}
		for _, item := range in.List {
			v, err := resolveValue(item, upstream)
			if err != nil {
				return nil, err
			}
			list = appendFlat(list, v)
		}
		return list, nil
	default:
		if len(in.Value) == 0 {
			return nil, nil
		}
		var v any
		if err := json.Unmarshal(in.Value, &v); err != nil {
			return nil, err
		}
		return v, nil
	}
}

func appendFlat(list []any, v any) []any {
	switch x := v.(type) {
	case nil:
		return list
	case string:
		if x == "" {
			return list
		}
		return append(list, x)
	case []any:
		for _, item := range x {
			list = appendFlat(list, item)
		}
		return list
	default:
		return append(list, x)
	}
}
