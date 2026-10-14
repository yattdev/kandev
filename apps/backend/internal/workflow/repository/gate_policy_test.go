package repository

import (
	"github.com/kandev/kandev/internal/testutil"
	"github.com/kandev/kandev/internal/workflow/models"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestCompletionGatePolicySurvivesRenameAndReplay(t *testing.T) {
	repo, _ := setupTestRepoWithDB(t)
	checkGateRenameAndReplay(t, repo)
}
func TestCompletionGatePolicyPostgres(t *testing.T) {
	repo := setupPostgresDecisionTestRepo(t, testutil.PostgresDSNFromEnv(t), 1)
	checkGateRenameAndReplay(t, repo)
}
func checkGateRenameAndReplay(t *testing.T, repo *Repository) {
	db := repo.db
	step := &models.WorkflowStep{WorkflowID: "wf-test", Name: "Review"}
	require.NoError(t, repo.CreateStep(t.Context(), step))
	saved, err := repo.GetStep(t.Context(), step.ID)
	require.NoError(t, err)
	require.Equal(t, models.CompletionGatePolicyReview, saved.CompletionGatePolicy)
	saved.Name = "Security check"
	require.NoError(t, repo.UpdateStep(t.Context(), saved))
	replay, err := NewWithDB(db, db, nil)
	require.NoError(t, err)
	got, err := replay.GetStep(t.Context(), step.ID)
	require.NoError(t, err)
	require.Equal(t, models.CompletionGatePolicyReview, got.CompletionGatePolicy)
	_, err = db.Exec(db.Rebind(`UPDATE workflow_steps SET name = ?, completion_gate_policy = '' WHERE id = ?`), "QA", step.ID)
	require.NoError(t, err)
	replay, err = NewWithDB(db, db, nil)
	require.NoError(t, err)
	migrated, err := replay.GetStep(t.Context(), step.ID)
	require.NoError(t, err)
	require.Equal(t, models.CompletionGatePolicyQA, migrated.CompletionGatePolicy)
	migrated.CompletionGatePolicy = models.CompletionGatePolicyNone
	require.NoError(t, replay.UpdateStep(t.Context(), migrated))
	replay, err = NewWithDB(db, db, nil)
	require.NoError(t, err)
	disabled, err := replay.GetStep(t.Context(), step.ID)
	require.NoError(t, err)
	require.Equal(t, models.CompletionGatePolicyNone, disabled.CompletionGatePolicy)
}
