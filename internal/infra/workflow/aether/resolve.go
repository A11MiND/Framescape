package aetherengine

import "encoding/json"

// ResolveExecutorType looks up which executor plugin type (e.g.
// "mock.image", "minimax.video") a template name maps to, by scanning the
// workflow's own JSON document. store.TaskRun carries TemplateType
// ("dag"/"task"/"loop") but not the executor type — that only exists in the
// task template's own "executor.type" field — so job_nodes' executor_type
// column (PRD §9.2) has to be resolved this way, not read off the TaskRun.
// Used by internal/application/projection; kept here (not there) so only
// this package parses aether/v1 documents.
func ResolveExecutorType(workflowJSON []byte, templateName string) string {
	var wf struct {
		Spec struct {
			Templates []struct {
				Task *struct {
					Name     string `json:"name"`
					Executor *struct {
						Type string `json:"type"`
					} `json:"executor"`
				} `json:"task"`
			} `json:"templates"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(workflowJSON, &wf); err != nil {
		return ""
	}
	for _, t := range wf.Spec.Templates {
		if t.Task != nil && t.Task.Name == templateName && t.Task.Executor != nil {
			return t.Task.Executor.Type
		}
	}
	return ""
}
