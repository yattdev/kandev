package handlers

import (
	"testing"

	wfmodels "github.com/kandev/kandev/internal/workflow/models"
	"github.com/stretchr/testify/require"
)

// Contract coverage for combining durable verdict policy with runnable moves.
func TestGateCompletionApplicabilityRequiresRunnableMove(t *testing.T) {
	for _, policy := range []wfmodels.CompletionGatePolicy{wfmodels.CompletionGatePolicyReview, wfmodels.CompletionGatePolicyQA} {
		for _, runnable := range []bool{false, true} {
			t.Run(string(policy)+map[bool]string{false: "/no-move", true: "/move"}[runnable], func(t *testing.T) {
				svc, repo := newTestTaskService(t)
				ctrl, wfRepo := newTestWorkflowController(t)
				step := &wfmodels.WorkflowStep{ID: "gate-applicability", WorkflowID: "wf", Name: "Renamed gate", CompletionGatePolicy: policy}
				if runnable {
					step.Events.OnTurnComplete = []wfmodels.OnTurnCompleteAction{{Type: wfmodels.OnTurnCompleteMoveToNext}}
				}
				require.NoError(t, wfRepo.CreateStep(t.Context(), step))
				h := newStepCompleteHandler(t, svc, repo, &mcpRecordingEventBus{})
				h.workflowCtrl = ctrl
				for _, verdict := range []string{"PASS", "BLOCKED", "FAILED", ""} {
					advances, note, ok := h.resolveStepCompletionAdvances(t.Context(), step.ID, "", verdict)
					require.True(t, ok)
					require.Equal(t, runnable && verdict == "PASS", advances)
					require.Equal(t, !advances, note != "")
				}
				advances, _, ok := h.resolveStepCompletionAdvances(t.Context(), step.ID, "unresolved blocker", "PASS")
				require.True(t, ok)
				require.False(t, advances)
			})
		}
	}
}
