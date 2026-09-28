package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kandev/kandev/internal/task/models"
)

// @covers AC-TASKS-SAFE-FORCE-REMOVAL-004
func TestClaimForceRemovalBlocksPromptableSessionClaim(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-prompt-ws", Name: "Force"}))
	for _, taskID := range []string{"force-prompt-held", "force-prompt-foreign", "force-prompt-stale"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: "force-prompt-ws", Title: taskID}))
		require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{ID: taskID + "-session", TaskID: taskID, State: models.TaskSessionStateWaitingForInput}))
	}

	held, err := repo.GetTask(ctx, "force-prompt-held")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{
		TaskID: held.ID, WorkspaceID: held.WorkspaceID, TaskGeneration: held.UpdatedAt,
		AdmissionGeneration: "admission", OperationID: "prompt-claim",
		RequestDigest: "request", PreviewDigest: "preview",
	})
	require.NoError(t, err)

	heldSession, err := repo.GetTaskSession(ctx, "force-prompt-held-session")
	require.NoError(t, err)
	claim, err := repo.ClaimPromptableTaskSessionIfActive(ctx, heldSession.ID)
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	require.Empty(t, claim)
	heldSession, err = repo.GetTaskSession(ctx, heldSession.ID)
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateWaitingForInput, heldSession.State)

	foreignSession, err := repo.GetTaskSession(ctx, "force-prompt-foreign-session")
	require.NoError(t, err)
	claim, err = repo.ClaimPromptableTaskSessionIfActive(ctx, foreignSession.ID)
	require.NoError(t, err)
	require.Equal(t, models.PromptableTaskSessionClaimed, claim.Status)

	staleSession, err := repo.GetTaskSession(ctx, "force-prompt-stale-session")
	require.NoError(t, err)
	claim, err = repo.ClaimPromptableTaskSessionIfActiveForIdentity(ctx, staleSession.TaskID, staleSession.ID, "stale-incarnation")
	require.NoError(t, err)
	require.Equal(t, models.PromptableTaskSessionInactive, claim.Status)
	staleSession, err = repo.GetTaskSession(ctx, staleSession.ID)
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateWaitingForInput, staleSession.State)
}
