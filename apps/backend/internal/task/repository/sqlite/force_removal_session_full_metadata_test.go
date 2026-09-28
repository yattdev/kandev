package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kandev/kandev/internal/task/models"
)

func TestClaimForceRemovalBlocksFullSessionMetadataUpdate(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-full-metadata-ws", Name: "Force"}))
	for _, taskID := range []string{"force-full-metadata-held", "force-full-metadata-foreign"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: "force-full-metadata-ws", Title: taskID}))
		require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{
			ID: taskID + "-session", TaskID: taskID, State: models.TaskSessionStateCreated,
			Metadata: map[string]interface{}{"owner": taskID + "-before"},
		}))
	}

	heldTask, err := repo.GetTask(ctx, "force-full-metadata-held")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{
		TaskID: heldTask.ID, WorkspaceID: heldTask.WorkspaceID, TaskGeneration: heldTask.UpdatedAt,
		AdmissionGeneration: "admission", OperationID: "full-metadata", RequestDigest: "request", PreviewDigest: "preview",
	})
	require.NoError(t, err)

	heldSession, err := repo.GetTaskSession(ctx, "force-full-metadata-held-session")
	require.NoError(t, err)
	heldSession.State = models.TaskSessionStateRunning
	err = repo.UpdateTaskSessionWithMetadata(ctx, heldSession, map[string]interface{}{"owner": "held-after"})
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	heldSession, err = repo.GetTaskSession(ctx, "force-full-metadata-held-session")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateCreated, heldSession.State)
	require.Equal(t, "force-full-metadata-held-before", heldSession.Metadata["owner"])

	foreignSession, err := repo.GetTaskSession(ctx, "force-full-metadata-foreign-session")
	require.NoError(t, err)
	foreignSession.State = models.TaskSessionStateRunning
	require.NoError(t, repo.UpdateTaskSessionWithMetadata(ctx, foreignSession, map[string]interface{}{"owner": "foreign-after"}))
	storedForeign, err := repo.GetTaskSession(ctx, "force-full-metadata-foreign-session")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateRunning, storedForeign.State)
	require.Equal(t, "foreign-after", storedForeign.Metadata["owner"])

	err = repo.UpdateTaskSessionWithMetadata(ctx, &models.TaskSession{ID: "force-full-metadata-missing-session"}, map[string]interface{}{})
	require.ErrorIs(t, err, models.ErrTaskSessionNotFound)
}
