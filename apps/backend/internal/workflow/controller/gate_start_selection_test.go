package controller

import (
	wfmodels "github.com/kandev/kandev/internal/workflow/models"
	"github.com/stretchr/testify/require"
	"testing"
)

// Contract coverage for policy edits racing an explicit start-step selection.
func TestGatePolicyEditPreservesConcurrentStartSelection(t *testing.T) {
	ctrl, writer, repo, provider := setupSelectionController(t, "other")
	step, err := repo.GetStep(t.Context(), "edited")
	require.NoError(t, err)
	step.CompletionGatePolicy = wfmodels.CompletionGatePolicyReview
	require.NoError(t, writer.UpdateStep(t.Context(), step))
	provider.before = func() {
		selected, loadErr := repo.GetStep(t.Context(), "edited")
		require.NoError(t, loadErr)
		selected.IsStartStep = true
		require.NoError(t, writer.UpdateStep(t.Context(), selected))
	}
	policy := wfmodels.CompletionGatePolicyQA
	name := "Renamed gate"
	resp, err := ctrl.UpdateStep(t.Context(), UpdateStepRequest{ID: "edited", Name: &name, CompletionGatePolicy: &policy})
	require.NoError(t, err)
	require.True(t, resp.Step.IsStartStep)
	saved, err := repo.GetStep(t.Context(), "edited")
	require.NoError(t, err)
	require.True(t, saved.IsStartStep)
	require.Equal(t, policy, saved.CompletionGatePolicy)
	require.Equal(t, name, saved.Name)
	other, err := repo.GetStep(t.Context(), "other")
	require.NoError(t, err)
	require.False(t, other.IsStartStep)
}
