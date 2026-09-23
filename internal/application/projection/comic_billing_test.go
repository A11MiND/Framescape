package projection

import (
	"context"
	"github.com/BabySid/aether/model"
	"github.com/BabySid/aether/store"
	"testing"
)

func TestFailedComicStorageStillSettlesKnownUsageOnce(t *testing.T) {
	env := newTestEnv(t, 100)
	p := New(env.db, nil, env.credits, nil, nil)
	tr := &store.TaskRun{RunID: "comic-paid-storage-failed-" + env.bizID, WorkflowRunID: env.runID, Status: phasePtr("Failed"), Outputs: &model.Outputs{ExecOutputs: model.ExecOutputs{Parameters: []model.Parameter{{Name: "cost-yuan", Value: []byte("1.75")}}}}}
	p.maybeCommitCredits(context.Background(), env.jobID, tr)
	var first int
	if err := env.db.QueryRow("SELECT credit_settled FROM jobs WHERE id = ?", env.jobID).Scan(&first); err != nil {
		t.Fatal(err)
	}
	if first <= 0 {
		t.Fatal("paid provider usage lost when materialization failed")
	}
	p.maybeCommitCredits(context.Background(), env.jobID, tr)
	var second int
	env.db.QueryRow("SELECT credit_settled FROM jobs WHERE id = ?", env.jobID).Scan(&second)
	if second != first {
		t.Fatal("duplicate settlement")
	}
}
