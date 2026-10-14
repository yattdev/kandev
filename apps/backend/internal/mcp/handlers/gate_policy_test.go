package handlers

import (
	"testing"

	workflowctrl "github.com/kandev/kandev/internal/workflow/controller"
	wfmodels "github.com/kandev/kandev/internal/workflow/models"
	ws "github.com/kandev/kandev/pkg/websocket"
	"github.com/stretchr/testify/require"
)

func TestMCPCompletionPolicySurvivesPartialRename(t *testing.T) {
	h, _, repo := setupImportHandlers(t)
	h.workflowCtrl = workflowctrl.NewController(h.workflowSvc)
	create := makeWSMessage(t, ws.ActionMCPCreateWorkflowStep, map[string]interface{}{"workflow_id": "wf-test", "name": "Quality check", "completion_gate_policy": "qa"})
	resp, err := h.handleCreateWorkflowStep(t.Context(), create)
	require.NoError(t, err)
	require.Equal(t, ws.MessageTypeResponse, resp.Type)
	steps, err := repo.ListStepsByWorkflow(t.Context(), "wf-test")
	require.NoError(t, err)
	require.Len(t, steps, 1)
	require.Equal(t, wfmodels.CompletionGatePolicyQA, steps[0].CompletionGatePolicy)
	update := makeWSMessage(t, ws.ActionMCPUpdateWorkflowStep, map[string]interface{}{"step_id": steps[0].ID, "name": "Renamed"})
	resp, err = h.handleUpdateWorkflowStep(t.Context(), update)
	require.NoError(t, err)
	require.Equal(t, ws.MessageTypeResponse, resp.Type)
	saved, err := repo.GetStep(t.Context(), steps[0].ID)
	require.NoError(t, err)
	require.Equal(t, wfmodels.CompletionGatePolicyQA, saved.CompletionGatePolicy)
}
