package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kandev/kandev/internal/task/models"
)

func TestClaimForceRemovalBlocksDirectSessionStateUpdate(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-direct-state-ws", Name: "Force"}))
	for _, taskID := range []string{"force-direct-state-held", "force-direct-state-foreign"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: "force-direct-state-ws", Title: taskID}))
		require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{
			ID: taskID + "-session", TaskID: taskID, State: models.TaskSessionStateCreated,
		}))
	}

	heldTask, err := repo.GetTask(ctx, "force-direct-state-held")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{
		TaskID: heldTask.ID, WorkspaceID: heldTask.WorkspaceID, TaskGeneration: heldTask.UpdatedAt,
		AdmissionGeneration: "admission", OperationID: "direct-state", RequestDigest: "request", PreviewDigest: "preview",
	})
	require.NoError(t, err)

	err = repo.UpdateTaskSessionState(ctx, "force-direct-state-held-session", models.TaskSessionStateCompleted, "held")
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	heldSession, err := repo.GetTaskSession(ctx, "force-direct-state-held-session")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateCreated, heldSession.State)
	require.Empty(t, heldSession.ErrorMessage)

	require.NoError(t, repo.UpdateTaskSessionState(ctx, "force-direct-state-foreign-session", models.TaskSessionStateCompleted, "foreign-terminal"))
	foreignSession, err := repo.GetTaskSession(ctx, "force-direct-state-foreign-session")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateCompleted, foreignSession.State)
	require.Equal(t, "foreign-terminal", foreignSession.ErrorMessage)

	// Direct state writes intentionally have no stale or terminal-state CAS.
	require.NoError(t, repo.UpdateTaskSessionState(ctx, "force-direct-state-foreign-session", models.TaskSessionStateCompleted, "foreign-terminal-update"))
	foreignSession, err = repo.GetTaskSession(ctx, "force-direct-state-foreign-session")
	require.NoError(t, err)
	require.Equal(t, "foreign-terminal-update", foreignSession.ErrorMessage)

	err = repo.UpdateTaskSessionState(ctx, "force-direct-state-missing-session", models.TaskSessionStateCompleted, "missing")
	require.ErrorIs(t, err, models.ErrTaskSessionNotFound)
}
