package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kandev/kandev/internal/task/models"
)

func TestClaimForceRemovalBlocksDirectFullSessionUpdate(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-full-update-ws", Name: "Force"}))
	for _, taskID := range []string{"force-full-update-held", "force-full-update-foreign"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: "force-full-update-ws", Title: taskID}))
		require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{
			ID: taskID + "-session", TaskID: taskID, State: models.TaskSessionStateCreated,
		}))
	}

	heldTask, err := repo.GetTask(ctx, "force-full-update-held")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{
		TaskID: heldTask.ID, WorkspaceID: heldTask.WorkspaceID, TaskGeneration: heldTask.UpdatedAt,
		AdmissionGeneration: "admission", OperationID: "full-update", RequestDigest: "request", PreviewDigest: "preview",
	})
	require.NoError(t, err)

	heldSession, err := repo.GetTaskSession(ctx, "force-full-update-held-session")
	require.NoError(t, err)
	heldSession.State = models.TaskSessionStateRunning
	heldSession.AgentProfileID = "held-profile"
	err = repo.UpdateTaskSession(ctx, heldSession)
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	heldSession, err = repo.GetTaskSession(ctx, "force-full-update-held-session")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateCreated, heldSession.State)
	require.Empty(t, heldSession.AgentProfileID)

	foreignSession, err := repo.GetTaskSession(ctx, "force-full-update-foreign-session")
	require.NoError(t, err)
	foreignSession.State = models.TaskSessionStateRunning
	foreignSession.AgentProfileID = "foreign-profile"
	require.NoError(t, repo.UpdateTaskSession(ctx, foreignSession))
	storedForeign, err := repo.GetTaskSession(ctx, "force-full-update-foreign-session")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateRunning, storedForeign.State)
	require.Equal(t, "foreign-profile", storedForeign.AgentProfileID)

	err = repo.UpdateTaskSession(ctx, &models.TaskSession{ID: "force-full-update-missing-session"})
	require.ErrorIs(t, err, models.ErrTaskSessionNotFound)
}
