package workflow

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// GateExecutor is the pseudo executor type of a human decision point. A gate
// node is never dispatched to a worker: it becomes Suspended as soon as its
// dependencies finish and completes when the job is resumed.
const GateExecutor = "gate"

// Success checks a node can require on top of the executor's own result code.
const (
	// CheckAllRequested fails an image node that returned fewer images than
	// it asked for (outputs success-count < requested-n).
	CheckAllRequested = "all_requested"
)

// Plan is the executable shape of one job: a DAG of nodes. Builders in the
// application layer produce it; the orchestrator persists and runs it.
type Plan struct {
	Nodes []NodeSpec
	// Deadline bounds how long the job may run, excluding time spent waiting
	// on a gate. Zero means the orchestrator default.
	Deadline time.Duration
	// GateTTL bounds how long a gate may wait for a decision before the job
	// is cancelled and its reservation released. Zero means the default.
	GateTTL time.Duration
	// Meta is builder-owned data needed later (for example on gate resume);
	// the orchestrator stores it verbatim.
	Meta json.RawMessage
}

// NodeSpec describes one step.
type NodeSpec struct {
	Name     string
	Executor string
	Inputs   map[string]Input
	// Deps lists extra dependencies. Nodes referenced by Inputs are
	// dependencies implicitly and need not be repeated here.
	Deps        []string
	MaxAttempts int
	Timeout     time.Duration
	Check       string
	// ReservedCredits is this node's share of the job quote; informational.
	ReservedCredits int
	// Display carries UI metadata such as a shot index or phase group.
	Display map[string]any
}

// Input is one executor parameter binding: a literal value, a reference to
// an upstream node's output, or a list assembled from several of those.
type Input struct {
	Value json.RawMessage `json:"v,omitempty"`
	Ref   *Ref            `json:"ref,omitempty"`
	List  []Input         `json:"list,omitempty"`
	// IsList marks an explicit (possibly empty) list binding.
	IsList bool `json:"is_list,omitempty"`
}

// Ref points at a named output of another node in the same job.
type Ref struct {
	Node   string `json:"node"`
	Output string `json:"output"`
}

// Lit binds a literal value. A nil slice is encoded as an empty JSON array
// so executors see [] rather than null.
func Lit(v any) Input {
	if s, ok := v.([]string); ok && s == nil {
		v = []string{}
	}
	raw, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("workflow.Lit: %v", err))
	}
	return Input{Value: raw}
}

// From binds another node's output.
func From(node, output string) Input {
	return Input{Ref: &Ref{Node: node, Output: output}}
}

// ListOf binds a JSON array built from items in order. Empty strings and
// null values are dropped and array-valued items are flattened, so optional
// references can be listed without special casing.
func ListOf(items ...Input) Input {
	return Input{List: items, IsList: true}
}

// Refs returns every node name this input depends on.
func (in Input) Refs() []string {
	var out []string
	if in.Ref != nil {
		out = append(out, in.Ref.Node)
	}
	for _, item := range in.List {
		out = append(out, item.Refs()...)
	}
	return out
}

// Dependencies returns the node's full, sorted, de-duplicated dependency set.
func (n NodeSpec) Dependencies() []string {
	set := map[string]bool{}
	for _, d := range n.Deps {
		set[d] = true
	}
	for _, in := range n.Inputs {
		for _, r := range in.Refs() {
			set[r] = true
		}
	}
	out := make([]string, 0, len(set))
	for d := range set {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// Validate checks that node names are unique and non-empty, every dependency
// exists (in this plan or in existing), no node depends on itself and the
// graph is acyclic. existing holds names of nodes already persisted for the
// job when validating a patch.
func Validate(nodes []NodeSpec, existing map[string]bool) error {
	byName := make(map[string]NodeSpec, len(nodes))
	for _, n := range nodes {
		if n.Name == "" {
			return fmt.Errorf("node with empty name")
		}
		if n.Executor == "" {
			return fmt.Errorf("node %q has no executor", n.Name)
		}
		if _, dup := byName[n.Name]; dup || existing[n.Name] {
			return fmt.Errorf("duplicate node name %q", n.Name)
		}
		byName[n.Name] = n
	}
	for _, n := range nodes {
		for _, d := range n.Dependencies() {
			if d == n.Name {
				return fmt.Errorf("node %q depends on itself", n.Name)
			}
			if _, ok := byName[d]; !ok && !existing[d] {
				return fmt.Errorf("node %q depends on unknown node %q", n.Name, d)
			}
		}
	}
	const (
		unvisited = iota
		visiting
		done
	)
	state := make(map[string]int, len(nodes))
	var visit func(name string) error
	visit = func(name string) error {
		switch state[name] {
		case visiting:
			return fmt.Errorf("dependency cycle through %q", name)
		case done:
			return nil
		}
		state[name] = visiting
		for _, d := range byName[name].Dependencies() {
			if _, inPlan := byName[d]; inPlan {
				if err := visit(d); err != nil {
					return err
				}
			}
		}
		state[name] = done
		return nil
	}
	for _, n := range nodes {
		if err := visit(n.Name); err != nil {
			return err
		}
	}
	return nil
}
