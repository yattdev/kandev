package orchestrator

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kandev/kandev/internal/task/models"
	wfmodels "github.com/kandev/kandev/internal/workflow/models"
)

func TestCompletionAdmissionDoesNotLeakToPublishedDestination(t *testing.T) {
	for _, cas := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy-store", true: "cas-store"}[cas], func(t *testing.T) {
			repo := setupTestRepo(t)
			seedSession(t, repo, "scope-task", "scope-session", "gate")
			getter := newMockStepGetter()
			getter.steps["next"] = &wfmodels.WorkflowStep{ID: "next", WorkflowID: "wf1", Name: "Next"}
			_, err := repo.CreateTurnWithStepStamp(t.Context(), &models.Turn{ID: "scope-turn", TaskID: "scope-task", TaskSessionID: "scope-session"})
			require.NoError(t, err)
			require.NoError(t, repo.SetSessionMetadataKey(t.Context(), "scope-session", models.SessionMetaKeyPendingStepCompletion, models.PendingStepCompletionSignal{StepID: "gate", TurnID: "scope-turn", Verdict: "PASS"}))
			ctx := models.WithStepCompletionAdmission(t.Context(), models.StepCompletionAdmission{TaskID: "scope-task", SessionID: "scope-session", StepID: "gate", TurnID: "scope-turn"})
			called := false
			publish := func(ctx context.Context, task *models.Task, _ ...string) {
				called = true
				next := *task
				next.WorkflowStepID = "final"
				_, moveErr := repo.UpdateTaskWithWorkflowStepAdmission(ctx, &next, "next", "final", 0)
				require.NoError(t, moveErr, "a destination move must not inherit its source's admission")
			}
			store := newWorkflowStore(repo, getter, nil, publish, testLogger(), &operationLedger{})
			if cas {
				_, err = store.ApplyTransitionIfAtStep(ctx, "scope-task", "scope-session", "gate", "next", "on_turn_complete")
			} else {
				err = store.ApplyTransition(ctx, "scope-task", "scope-session", "gate", "next", "on_turn_complete")
			}
			require.NoError(t, err)
			require.True(t, called)
			saved, err := repo.GetTask(t.Context(), "scope-task")
			require.NoError(t, err)
			require.Equal(t, "final", saved.WorkflowStepID)
		})
	}
}
