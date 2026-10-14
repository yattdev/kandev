package handlers

import (
	"github.com/kandev/kandev/internal/task/models"
	wfmodels "github.com/kandev/kandev/internal/workflow/models"
	ws "github.com/kandev/kandev/pkg/websocket"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestStepCompleteRejectsOldTurnAfterSameStepRetry(t *testing.T) {
	svc, repo := newTestTaskService(t)
	seedStepCompleteTarget(t, repo, "task-generation", "session-generation", "step-review", models.TaskSessionStateRunning)
	createStepCompleteRecoveryTurn(t, repo, "turn-old", "task-generation", "session-generation")
	createStepCompleteRecoveryTurn(t, repo, "turn-new", "task-generation", "session-generation")
	bus := &mcpRecordingEventBus{}
	h := newStepCompleteHandler(t, svc, repo, bus)
	old := makeWSMessage(t, ws.ActionMCPStepComplete, map[string]interface{}{"task_id": "task-generation", "session_id": "session-generation", "turn_id": "turn-old", "summary": "late PASS", "verdict": "PASS"})
	resp, err := h.handleStepComplete(t.Context(), old)
	require.NoError(t, err)
	assertWSError(t, resp, ws.ErrorCodeValidation)
	require.Empty(t, bus.events)
	session, err := repo.GetTaskSession(t.Context(), "session-generation")
	require.NoError(t, err)
	_, has := models.LoadPendingStepSignal(session.Metadata)
	require.False(t, has)
	fresh := makeWSMessage(t, ws.ActionMCPStepComplete, map[string]interface{}{"task_id": "task-generation", "session_id": "session-generation", "turn_id": "turn-new", "summary": "current PASS", "verdict": "PASS"})
	resp, err = h.handleStepComplete(t.Context(), fresh)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Len(t, bus.events, 1)
}

// Contract coverage: gate signals must carry the exact prompt turn identity.
func TestGateSignalRequiresCallingTurn(t *testing.T) {
	for _, policy := range []wfmodels.CompletionGatePolicy{wfmodels.CompletionGatePolicyReview, wfmodels.CompletionGatePolicyQA} {
		t.Run(string(policy), func(t *testing.T) {
			svc, repo := newTestTaskService(t)
			seedStepCompleteTarget(t, repo, "task-bound", "session-bound", "gate-bound", models.TaskSessionStateRunning)
			createStepCompleteRecoveryTurn(t, repo, "turn-bound", "task-bound", "session-bound")
			ctrl, wfRepo := newTestWorkflowController(t)
			require.NoError(t, wfRepo.CreateStep(t.Context(), &wfmodels.WorkflowStep{ID: "gate-bound", WorkflowID: "wf", Name: "Renamed", CompletionGatePolicy: policy}))
			bus := &mcpRecordingEventBus{}
			h := newStepCompleteHandler(t, svc, repo, bus)
			h.workflowCtrl = ctrl
			missing := makeWSMessage(t, ws.ActionMCPStepComplete, map[string]interface{}{"task_id": "task-bound", "session_id": "session-bound", "summary": "PASS", "verdict": "PASS"})
			resp, err := h.handleStepComplete(t.Context(), missing)
			require.NoError(t, err)
			assertWSError(t, resp, ws.ErrorCodeValidation)
			require.Empty(t, bus.events)
			valid := makeWSMessage(t, ws.ActionMCPStepComplete, map[string]interface{}{"task_id": "task-bound", "session_id": "session-bound", "turn_id": "turn-bound", "summary": "PASS", "verdict": "PASS"})
			resp, err = h.handleStepComplete(t.Context(), valid)
			require.NoError(t, err)
			require.Equal(t, ws.MessageTypeResponse, resp.Type)
			require.Len(t, bus.events, 1)
		})
	}
}
