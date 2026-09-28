package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kandev/kandev/internal/task/models"
)

func TestClaimForceRemovalBlocksSessionStartAttemptCAS(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-start-attempt-ws", Name: "Force"}))
	for _, taskID := range []string{"force-start-attempt-held", "force-start-attempt-foreign"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: "force-start-attempt-ws", Title: taskID}))
		require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{
			ID: taskID + "-session", TaskID: taskID, State: models.TaskSessionStateCreated,
		}))
	}

	heldTask, err := repo.GetTask(ctx, "force-start-attempt-held")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{
		TaskID: heldTask.ID, WorkspaceID: heldTask.WorkspaceID, TaskGeneration: heldTask.UpdatedAt,
		AdmissionGeneration: "admission", OperationID: "start-attempt", RequestDigest: "request", PreviewDigest: "preview",
	})
	require.NoError(t, err)

	heldSession, err := repo.GetTaskSession(ctx, "force-start-attempt-held-session")
	require.NoError(t, err)
	heldSession.State = models.TaskSessionStateStarting
	changed, err := repo.UpdateTaskSessionIfCurrentStateWithStartAttempt(ctx, heldSession, models.TaskSessionStateCreated, "held-attempt")
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	require.False(t, changed)
	heldSession, err = repo.GetTaskSession(ctx, "force-start-attempt-held-session")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateCreated, heldSession.State)
	require.NotContains(t, heldSession.Metadata, models.SessionMetaKeyAgentStartAttemptID)

	foreignSession, err := repo.GetTaskSession(ctx, "force-start-attempt-foreign-session")
	require.NoError(t, err)
	foreignSession.State = models.TaskSessionStateStarting
	changed, err = repo.UpdateTaskSessionIfCurrentStateWithStartAttempt(ctx, foreignSession, models.TaskSessionStateCreated, "foreign-attempt")
	require.NoError(t, err)
	require.True(t, changed)
	foreignSession.State = models.TaskSessionStateRunning
	changed, err = repo.UpdateTaskSessionIfCurrentStateWithStartAttempt(ctx, foreignSession, models.TaskSessionStateCreated, "stale-attempt")
	require.NoError(t, err)
	require.False(t, changed)
	storedForeign, err := repo.GetTaskSession(ctx, "force-start-attempt-foreign-session")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateStarting, storedForeign.State)
	require.Equal(t, "foreign-attempt", storedForeign.Metadata[models.SessionMetaKeyAgentStartAttemptID])
}
