package orchestrator

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/kandev/kandev/internal/task/models"
	sqliterepo "github.com/kandev/kandev/internal/task/repository/sqlite"
	wfmodels "github.com/kandev/kandev/internal/workflow/models"
)

type completionRevisitAfterValidation struct {
	*sqliterepo.Repository
	t     *testing.T
	fired bool
}

func (r *completionRevisitAfterValidation) CompletionTurnCurrent(ctx context.Context, taskID, sessionID, stepID, turnID string) (bool, error) {
	current, err := r.Repository.CompletionTurnCurrent(ctx, taskID, sessionID, stepID, turnID)
	if err != nil || !current || r.fired {
		return current, err
	}
	r.fired = true
	task, err := r.GetTask(ctx, taskID)
	require.NoError(r.t, err)
	task.WorkflowStepID = "next"
	require.NoError(r.t, r.UpdateTask(ctx, task))
	task.WorkflowStepID = stepID
	require.NoError(r.t, r.UpdateTask(ctx, task))
	return current, nil
}

func TestCompletionCommitRejectsRevisitedGeneration(t *testing.T) {
	for _, engine := range []bool{false, true} {
		for _, policy := range []wfmodels.CompletionGatePolicy{wfmodels.CompletionGatePolicyReview, wfmodels.CompletionGatePolicyQA} {
			t.Run(fmt.Sprintf("engine=%t/%s", engine, policy), func(t *testing.T) { checkCompletionRevisitAtCommit(t, engine, policy) })
		}
	}
}

func checkCompletionRevisitAtCommit(t *testing.T, engine bool, policy wfmodels.CompletionGatePolicy) {
	t.Helper()
	repo := setupTestRepo(t)
	seedSession(t, repo, "race-task", "race-session", "gate")
	getter := newMockStepGetter()
	getter.steps["gate"] = &wfmodels.WorkflowStep{ID: "gate", WorkflowID: "wf1", Name: "Renamed gate", Position: 1, CompletionGatePolicy: policy, Events: wfmodels.StepEvents{OnTurnComplete: []wfmodels.OnTurnCompleteAction{{Type: wfmodels.OnTurnCompleteMoveToNext}}}}
	getter.steps["next"] = &wfmodels.WorkflowStep{ID: "next", WorkflowID: "wf1", Name: "Next", Position: 2}
	svc := createTestService(repo, getter, newMockTaskRepo())
	if engine {
		svc = createEngineService(t, repo, getter, &mockAgentManager{})
	}
	_, err := repo.CreateTurnWithStepStamp(t.Context(), &models.Turn{ID: "race-turn", TaskID: "race-task", TaskSessionID: "race-session"})
	require.NoError(t, err)
	require.NoError(t, repo.CompleteTurn(t.Context(), "race-turn"))
	signal := models.PendingStepCompletionSignal{StepID: "gate", TurnID: "race-turn", Verdict: "PASS"}
	stored, err := repo.SetSessionMetadataKeyIfAbsentOrDifferentStepIfTaskAtStep(t.Context(), "race-task", "race-session", models.SessionMetaKeyPendingStepCompletion, "gate", signal)
	require.NoError(t, err)
	require.True(t, stored)
	session, err := repo.GetTaskSession(t.Context(), "race-session")
	require.NoError(t, err)
	session.State = models.TaskSessionStateWaitingForInput
	require.NoError(t, repo.UpdateTaskSession(t.Context(), session))
	barrier := &completionRevisitAfterValidation{Repository: repo, t: t}
	svc.repo = barrier
	entered := make(chan struct{}, 1)
	if engine {
		svc.onProcessOnEnterComplete = func() { entered <- struct{}{} }
	}
	svc.onStepCompletionSignaled(t.Context(), stepCompletionSignalEvent("race-task", "race-session", "gate"))
	require.True(t, barrier.fired)
	saved, err := repo.GetTask(t.Context(), "race-task")
	require.NoError(t, err)
	if engine && saved.WorkflowStepID == "next" {
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatal("on_enter did not drain")
		}
	}
	require.Equal(t, "gate", saved.WorkflowStepID)
	persisted, err := repo.GetTaskSession(t.Context(), session.ID)
	require.NoError(t, err)
	pending, has := models.LoadPendingStepSignal(persisted.Metadata)
	require.True(t, has)
	require.Equal(t, signal.TurnID, pending.TurnID)
}
