package orchestrator

import (
	"fmt"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/task/models"
	wfmodels "github.com/kandev/kandev/internal/workflow/models"
	"github.com/stretchr/testify/require"
)

func TestPersistedGateCannotBeBypassed(t *testing.T) {
	for _, policy := range []wfmodels.CompletionGatePolicy{wfmodels.CompletionGatePolicyReview, wfmodels.CompletionGatePolicyQA} {
		for _, engine := range []bool{false, true} {
			for _, cancel := range []bool{false, true} {
				for _, verdict := range []string{"", "BLOCKED", "FAILED", "PASS"} {
					t.Run(fmt.Sprintf("%s/engine=%t/cancel=%t/%s", policy, engine, cancel, verdict), func(t *testing.T) {
						runPersistedGateCase(t, policy, engine, cancel, verdict, "gate-turn", false, verdict == "PASS")
					})
				}
			}
		}
	}
}

func TestPersistedGateRejectsPreviousGeneration(t *testing.T) {
	for _, engine := range []bool{false, true} {
		for _, turnID := range []string{"", "gate-turn"} {
			t.Run(fmt.Sprintf("engine=%t/turn=%s", engine, turnID), func(t *testing.T) {
				runPersistedGateCase(t, wfmodels.CompletionGatePolicyReview, engine, false, "PASS", turnID, true, false)
			})
		}
	}
}

func runPersistedGateCase(t *testing.T, policy wfmodels.CompletionGatePolicy, engine, cancel bool, verdict, turnID string, retry, wantMove bool) {
	t.Helper()
	repo := setupTestRepo(t)
	seedSession(t, repo, "gate-task", "gate-session", "gate")
	getter := newMockStepGetter()
	getter.steps["gate"] = &wfmodels.WorkflowStep{ID: "gate", WorkflowID: "wf1", Name: "Renamed check", Position: 1, CompletionGatePolicy: policy, CancelTriggersTurnComplete: true, Events: wfmodels.StepEvents{OnTurnComplete: []wfmodels.OnTurnCompleteAction{{Type: wfmodels.OnTurnCompleteMoveToNext}}}}
	getter.steps["next"] = &wfmodels.WorkflowStep{ID: "next", WorkflowID: "wf1", Name: "Next", Position: 2}
	svc := createTestService(repo, getter, newMockTaskRepo())
	if engine {
		svc = createEngineService(t, repo, getter, &mockAgentManager{})
	}
	_, err := repo.CreateTurnWithStepStamp(t.Context(), &models.Turn{ID: "gate-turn", TaskID: "gate-task", TaskSessionID: "gate-session"})
	require.NoError(t, err)
	require.NoError(t, repo.SetSessionMetadataKey(t.Context(), "gate-session", models.SessionMetaKeyPendingStepCompletion, models.PendingStepCompletionSignal{StepID: "gate", TurnID: turnID, Verdict: verdict, SignaledAt: time.Now().UTC()}))
	if retry {
		_, err = repo.CreateTurnWithStepStamp(t.Context(), &models.Turn{ID: "retry-turn", TaskID: "gate-task", TaskSessionID: "gate-session"})
		require.NoError(t, err)
	}
	task, err := repo.GetTask(t.Context(), "gate-task")
	require.NoError(t, err)
	session, err := repo.GetTaskSession(t.Context(), "gate-session")
	require.NoError(t, err)
	cause := turnCompletionCauseAgentTurn
	if cancel {
		cause = turnCompletionCauseUserCancellation
	}
	var moved bool
	if engine {
		moved = svc.processOnTurnCompleteViaEngineWithCause(t.Context(), task.ID, session, cause)
	} else {
		moved = svc.processOnTurnCompleteWithCause(t.Context(), task, session, cause)
	}
	require.Equal(t, wantMove, moved)
	saved, err := repo.GetTask(t.Context(), task.ID)
	require.NoError(t, err)
	want := "gate"
	if wantMove {
		want = "next"
	}
	require.Equal(t, want, saved.WorkflowStepID)
}
