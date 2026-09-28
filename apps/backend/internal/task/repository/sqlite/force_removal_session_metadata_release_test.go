package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kandev/kandev/internal/task/models"
)

func TestClaimForceRemovalBlocksFullSessionMetadataReleaseCAS(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-metadata-release-ws", Name: "Force"}))
	for _, taskID := range []string{"force-metadata-release-held", "force-metadata-release-foreign"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: "force-metadata-release-ws", Title: taskID}))
		require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{
			ID: taskID + "-session", TaskID: taskID, State: models.TaskSessionStateCreated,
			Metadata: map[string]interface{}{"provider": taskID + "-provider", "keep": taskID + "-keep"},
		}))
	}

	heldTask, err := repo.GetTask(ctx, "force-metadata-release-held")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{
		TaskID: heldTask.ID, WorkspaceID: heldTask.WorkspaceID, TaskGeneration: heldTask.UpdatedAt,
		AdmissionGeneration: "admission", OperationID: "metadata-release", RequestDigest: "request", PreviewDigest: "preview",
	})
	require.NoError(t, err)

	heldSession, err := repo.GetTaskSession(ctx, "force-metadata-release-held-session")
	require.NoError(t, err)
	heldSession.State = models.TaskSessionStateRunning
	changed, err := repo.UpdateTaskSessionIfCurrentStateRemovingMetadataKeys(ctx, heldSession, models.TaskSessionStateCreated, []string{"provider"})
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	require.False(t, changed)
	heldSession, err = repo.GetTaskSession(ctx, "force-metadata-release-held-session")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateCreated, heldSession.State)
	require.Equal(t, "force-metadata-release-held-provider", heldSession.Metadata["provider"])

	foreignSession, err := repo.GetTaskSession(ctx, "force-metadata-release-foreign-session")
	require.NoError(t, err)
	foreignSession.State = models.TaskSessionStateRunning
	changed, err = repo.UpdateTaskSessionIfCurrentStateRemovingMetadataKeys(ctx, foreignSession, models.TaskSessionStateCreated, []string{"provider"})
	require.NoError(t, err)
	require.True(t, changed)
	foreignSession.State = models.TaskSessionStateCompleted
	changed, err = repo.UpdateTaskSessionIfCurrentStateRemovingMetadataKeys(ctx, foreignSession, models.TaskSessionStateCreated, []string{"keep"})
	require.NoError(t, err)
	require.False(t, changed)
	storedForeign, err := repo.GetTaskSession(ctx, "force-metadata-release-foreign-session")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateRunning, storedForeign.State)
	require.NotContains(t, storedForeign.Metadata, "provider")
	require.Equal(t, "force-metadata-release-foreign-keep", storedForeign.Metadata["keep"])
}
