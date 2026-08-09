// gate.go is human.gate (PRD §10.3 pattern, DEV_PLAN.md §10): the preview
// gate primitive video.sequence's "draft → gate → upgrade → concat" skeleton
// suspends on. It carries no business logic of its own — F5.5/§13.4's
// bucketing (which shots get redone/upgraded/kept) is real Go code that runs
// in jobsvc.Resume before it ever reaches this executor; gate's only job is
// to block the DAG until that decision exists, then hand it downstream
// verbatim so the redo/regen Loop templates can read it via
// tasks.gate.outputs.parameters.*.
package local

import (
	"context"

	"github.com/BabySid/aether/executor"
	"github.com/BabySid/aether/model"
)

// decisionMarker is the one input key Resume's payload always includes,
// purely so Execute can tell "first dispatch, no decision yet" apart from
// "re-dispatched via Resume with a real decision" — the actual bucketing
// fields (redo-items/upgrade-items/keep-items) ride along in the same
// payload but their names are business-defined, not something this
// executor needs to know about to do its job.
const decisionMarker = "decision"

type GatePlugin struct{}

func NewGatePlugin() *GatePlugin { return &GatePlugin{} }

func (p *GatePlugin) Type() string { return "human.gate" }

func (p *GatePlugin) Schema() model.ExecutorSchema {
	return executor.SchemaOf[executor.DynamicOutputs, executor.DynamicOutputs](
		"human.gate", "1.0", "Suspend until POST /jobs/{bizID}/resume supplies a decision (PRD §13.4)",
	)
}

func (p *GatePlugin) Execute(_ context.Context, req *executor.ExecuteRequest) (*model.ExecOutputs, error) {
	decided := false
	var params []model.Parameter
	if req.Inputs != nil {
		params = req.Inputs.Parameters
		for _, ip := range params {
			if ip.Name == decisionMarker {
				decided = true
				break
			}
		}
	}
	if !decided {
		return &model.ExecOutputs{Code: model.ExecCodeSuspended}, nil
	}

	// Pass every merged input straight through as this task's outputs — the
	// engine's Resume() already merged the resolved payload into req.Inputs
	// (last-writer-wins on top of anything from the original dispatch), so
	// echoing it back is the whole job.
	out := make([]model.Parameter, len(params))
	copy(out, params)
	return &model.ExecOutputs{Code: model.ExecCodeSucceeded, Parameters: out}, nil
}

var _ executor.Plugin = (*GatePlugin)(nil)
