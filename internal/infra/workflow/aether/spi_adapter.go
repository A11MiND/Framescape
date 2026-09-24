package aetherengine

import (
	"context"
	"encoding/json"

	"github.com/BabySid/aether/executor"
	"github.com/BabySid/aether/model"

	spiexecutor "aigc-platform/internal/infra/executor/spi/executor"
	spimodel "aigc-platform/internal/infra/executor/spi/model"
)

// AdaptRegistry exposes engine-independent executor plugins to the vendored
// engine. The two parameter models share the same JSON shape, so values are
// copied field by field.
func AdaptRegistry(src *spiexecutor.Registry) (*executor.Registry, error) {
	dst := executor.NewRegistry()
	for _, t := range src.Types() {
		p, _ := src.Get(t)
		if err := dst.Register(spiPlugin{p}); err != nil {
			return nil, err
		}
	}
	return dst, nil
}

type spiPlugin struct{ p spiexecutor.Plugin }

func (a spiPlugin) Type() string { return a.p.Type() }

func (a spiPlugin) Schema() model.ExecutorSchema {
	var out model.ExecutorSchema
	raw, _ := json.Marshal(a.p.Schema())
	_ = json.Unmarshal(raw, &out)
	return out
}

func (a spiPlugin) Execute(ctx context.Context, req *executor.ExecuteRequest) (*model.ExecOutputs, error) {
	in := &spimodel.Inputs{}
	if req.Inputs != nil {
		for _, p := range req.Inputs.Parameters {
			in.Parameters = append(in.Parameters, spimodel.Parameter{Name: p.Name, Type: p.Type, Value: p.Value})
		}
	}
	out, err := a.p.Execute(ctx, &spiexecutor.ExecuteRequest{
		TaskRunID: req.TaskRunID, WorkflowRunID: req.WorkflowRunID, TaskName: req.TaskName,
		TemplateName: req.TemplateName, Inputs: in, Timeout: req.Timeout, RetryCount: req.RetryCount,
	})
	if out == nil {
		return nil, err
	}
	res := &model.ExecOutputs{Code: out.Code, Message: out.Message}
	for _, p := range out.Parameters {
		res.Parameters = append(res.Parameters, model.Parameter{Name: p.Name, Type: p.Type, Value: p.Value})
	}
	return res, err
}
