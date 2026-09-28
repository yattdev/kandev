package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kandev/kandev/internal/task/models"
)

func TestClaimForceRemovalBlocksFullSessionStateCAS(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-full-state-ws", Name: "Force"}))
	for _, taskID := range []string{"force-full-state-held", "force-full-state-foreign"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: "force-full-state-ws", Title: taskID}))
		require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{
			ID: taskID + "-session", TaskID: taskID, State: models.TaskSessionStateCreated,
		}))
	}

	heldTask, err := repo.GetTask(ctx, "force-full-state-held")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{
		TaskID: heldTask.ID, WorkspaceID: heldTask.WorkspaceID, TaskGeneration: heldTask.UpdatedAt,
		AdmissionGeneration: "admission", OperationID: "full-state", RequestDigest: "request", PreviewDigest: "preview",
	})
	require.NoError(t, err)

	heldSession, err := repo.GetTaskSession(ctx, "force-full-state-held-session")
	require.NoError(t, err)
	heldSession.State = models.TaskSessionStateRunning
	changed, err := repo.UpdateTaskSessionIfCurrentState(ctx, heldSession, models.TaskSessionStateCreated)
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	require.False(t, changed)
	heldSession, err = repo.GetTaskSession(ctx, "force-full-state-held-session")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateCreated, heldSession.State)

	foreignSession, err := repo.GetTaskSession(ctx, "force-full-state-foreign-session")
	require.NoError(t, err)
	foreignSession.State = models.TaskSessionStateRunning
	changed, err = repo.UpdateTaskSessionIfCurrentState(ctx, foreignSession, models.TaskSessionStateCreated)
	require.NoError(t, err)
	require.True(t, changed)
	foreignSession.State = models.TaskSessionStateCompleted
	changed, err = repo.UpdateTaskSessionIfCurrentState(ctx, foreignSession, models.TaskSessionStateCreated)
	require.NoError(t, err)
	require.False(t, changed)
	storedForeign, err := repo.GetTaskSession(ctx, "force-full-state-foreign-session")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateRunning, storedForeign.State)
}
