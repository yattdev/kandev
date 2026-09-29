package sqlite

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

func TestClaimForceRemovalBlocksTaskTitleUpdate(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	const workspaceID = "force-title-workspace"
	const heldTaskID = "force-title-held"
	const foreignTaskID = "force-title-foreign"
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: workspaceID, Name: "Force"}))
	for _, taskID := range []string{heldTaskID, foreignTaskID} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: workspaceID, Title: "Provisional title"}))
		require.NoError(t, repo.SetTaskMetadataKey(ctx, taskID, models.MetaKeyAgentTitlePending, true))
		require.NoError(t, repo.SetTaskMetadataKey(ctx, taskID, models.MetaKeyAgentTitleOwnerSessionID, "title-session"))
	}
	heldTask, err := repo.GetTask(ctx, heldTaskID)
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{
		TaskID: heldTask.ID, WorkspaceID: heldTask.WorkspaceID, TaskGeneration: heldTask.UpdatedAt,
		AdmissionGeneration: "admission", OperationID: "title-update", RequestDigest: "request", PreviewDigest: "preview",
	})
	require.NoError(t, err)

	accepted, err := repo.SetTaskTitleIfPending(ctx, heldTaskID, "title-session", "Must not persist")
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	require.False(t, accepted)
	heldAfter, err := repo.GetTask(ctx, heldTaskID)
	require.NoError(t, err)
	require.Equal(t, "Provisional title", heldAfter.Title)
	require.Equal(t, true, heldAfter.Metadata[models.MetaKeyAgentTitlePending])

	accepted, err = repo.SetTaskTitleIfPending(ctx, foreignTaskID, "title-session", "Foreign title")
	require.NoError(t, err)
	require.True(t, accepted)
	foreignAfter, err := repo.GetTask(ctx, foreignTaskID)
	require.NoError(t, err)
	require.Equal(t, "Foreign title", foreignAfter.Title)
}
