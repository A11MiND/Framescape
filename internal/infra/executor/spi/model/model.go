// Package model holds the executor data contract: parameters, inputs and the
// outputs an executor returns. Derived from github.com/BabySid/aether's model
// package (BSD-3-Clause, Copyright (c) 2026, Master Sid; see
// ../LICENSE.aether), reduced to what executors actually use so they no
// longer depend on the orchestration engine.
package model

import "encoding/json"

// Executor result codes.
const (
	ExecCodeSucceeded = 0
	// ExecCodeSuspended means the task waits for an external decision.
	ExecCodeSuspended = 1
	// ExecCodeFailed is a business failure (e.g. content rejected); not retried.
	ExecCodeFailed = 2
	// ExecCodeError is a system-level failure; retried within the node's budget.
	ExecCodeError = 3
	// ExecCodeTimeout means the task exceeded its deadline; retried like Error.
	ExecCodeTimeout = 4
)

// Parameter is one named input or output value. Value holds raw JSON.
type Parameter struct {
	Name        string          `json:"name"`
	Type        string          `json:"type,omitempty"`
	Value       json.RawMessage `json:"value,omitempty"`
	Description string          `json:"description,omitempty"`
}

// Inputs is the set of parameters handed to an executor.
type Inputs struct {
	Parameters []Parameter `json:"parameters,omitempty"`
}

// ExecOutputs is what an executor returns.
type ExecOutputs struct {
	Code       int         `json:"code,omitempty"`
	Message    string      `json:"message,omitempty"`
	Parameters []Parameter `json:"parameters,omitempty"`
}

// ExecutorSchema is an executor's self-description.
type ExecutorSchema struct {
	Type        string       `json:"type"`
	Version     string       `json:"version"`
	Description string       `json:"description,omitempty"`
	Inputs      *Inputs      `json:"inputs,omitempty"`
	Outputs     *ExecOutputs `json:"outputs,omitempty"`
}

// ParamsToMap decodes each parameter's JSON value; values that fail to
// decode are kept as their raw string.
func ParamsToMap(params []Parameter) map[string]any {
	out := make(map[string]any, len(params))
	for _, p := range params {
		var v any
		if err := json.Unmarshal(p.Value, &v); err != nil {
			v = string(p.Value)
		}
		out[p.Name] = v
	}
	return out
}
