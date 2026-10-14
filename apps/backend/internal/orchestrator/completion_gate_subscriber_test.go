package orchestrator

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/kandev/kandev/internal/task/models"
	wfmodels "github.com/kandev/kandev/internal/workflow/models"
)

func TestPersistedGateLateSignalWithoutLegacyFlag(t *testing.T) {
	for _, policy := range []wfmodels.CompletionGatePolicy{wfmodels.CompletionGatePolicyReview, wfmodels.CompletionGatePolicyQA, wfmodels.CompletionGatePolicyNone} {
		for _, engine := range []bool{false, true} {
			for _, verdict := range []string{"PASS", "BLOCKED", "FAILED", ""} {
				t.Run(fmt.Sprintf("%s/engine=%t/%s", policy, engine, verdict), func(t *testing.T) {
					checkLateGateSignal(t, policy, engine, verdict)
				})
			}
		}
	}
}

func checkLateGateSignal(t *testing.T, policy wfmodels.CompletionGatePolicy, engine bool, verdict string) {
	t.Helper()
	repo := setupTestRepo(t)
	seedSession(t, repo, "late-task", "late-session", "gate")
	getter := newMockStepGetter()
	getter.steps["gate"] = &wfmodels.WorkflowStep{
		ID: "gate", WorkflowID: "wf1", Name: "Renamed gate", Position: 1, CompletionGatePolicy: policy,
		Events: wfmodels.StepEvents{OnTurnComplete: []wfmodels.OnTurnCompleteAction{{Type: wfmodels.OnTurnCompleteMoveToNext}}},
	}
	getter.steps["next"] = &wfmodels.WorkflowStep{ID: "next", WorkflowID: "wf1", Name: "Next", Position: 2}
	svc := createTestService(repo, getter, newMockTaskRepo())
	if engine {
		svc = createEngineService(t, repo, getter, &mockAgentManager{})
	}
	_, err := repo.CreateTurnWithStepStamp(t.Context(), &models.Turn{ID: "late-turn", TaskID: "late-task", TaskSessionID: "late-session"})
	require.NoError(t, err)
	require.NoError(t, repo.CompleteTurn(t.Context(), "late-turn"))
	session, err := repo.GetTaskSession(t.Context(), "late-session")
	require.NoError(t, err)
	session.State = models.TaskSessionStateWaitingForInput
	require.NoError(t, repo.UpdateTaskSession(t.Context(), session))
	signal := models.PendingStepCompletionSignal{StepID: "gate", TurnID: "late-turn", Verdict: verdict, Source: models.StepCompletionSourceAgent, SignaledAt: time.Now().UTC()}
	stored, err := repo.SetSessionMetadataKeyIfAbsentOrDifferentStepIfTaskAtStep(t.Context(), "late-task", "late-session", models.SessionMetaKeyPendingStepCompletion, "gate", signal)
	require.NoError(t, err)
	require.True(t, stored)
	svc.onStepCompletionSignaled(t.Context(), stepCompletionSignalEvent("late-task", "late-session", "gate"))
	task, err := repo.GetTask(t.Context(), "late-task")
	require.NoError(t, err)
	want := "gate"
	if policy != wfmodels.CompletionGatePolicyNone && verdict == "PASS" {
		want = "next"
	}
	require.Equal(t, want, task.WorkflowStepID)
}
