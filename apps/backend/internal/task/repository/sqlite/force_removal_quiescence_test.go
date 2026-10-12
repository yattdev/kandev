package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kandev/kandev/internal/task/models"
)

func TestForceRemovalQuiescenceCancellationRequiresExactClaim(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-quiesce-ws", Name: "Force"}))
	for _, taskID := range []string{"force-quiesce-held", "force-quiesce-foreign"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: "force-quiesce-ws", Title: taskID}))
		require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{ID: taskID + "-session", TaskID: taskID, State: models.TaskSessionStateRunning}))
	}
	held, err := repo.GetTask(ctx, "force-quiesce-held")
	require.NoError(t, err)
	claim := &models.ForceRemovalClaim{TaskID: held.ID, WorkspaceID: held.WorkspaceID, TaskGeneration: held.UpdatedAt, AdmissionGeneration: "admission", OperationID: "quiesce-operation", RequestDigest: "request", PreviewDigest: "preview"}
	_, _, err = repo.ClaimForceRemoval(ctx, claim)
	require.NoError(t, err)

	_, err = repo.CancelActiveTaskSessionsForForceRemoval(ctx, held.ID, held.WorkspaceID, "foreign-operation", held.UpdatedAt, "force removal")
	require.ErrorIs(t, err, ErrForceRemovalClaimStale)
	heldSession, err := repo.GetTaskSession(ctx, "force-quiesce-held-session")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateRunning, heldSession.State)

	foreign, err := repo.GetTask(ctx, "force-quiesce-foreign")
	require.NoError(t, err)
	_, err = repo.CancelActiveTaskSessionsForForceRemoval(ctx, foreign.ID, foreign.WorkspaceID, claim.OperationID, foreign.UpdatedAt, "force removal")
	require.ErrorIs(t, err, ErrForceRemovalClaimStale)
	foreignSession, err := repo.GetTaskSession(ctx, "force-quiesce-foreign-session")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateRunning, foreignSession.State)

	cancelled, err := repo.CancelActiveTaskSessionsForForceRemoval(ctx, held.ID, held.WorkspaceID, claim.OperationID, held.UpdatedAt, "force removal")
	require.NoError(t, err)
	require.Len(t, cancelled, 1)
	require.Equal(t, models.TaskSessionStateCancelled, cancelled[0].State)
	foreignSession, err = repo.GetTaskSession(ctx, "force-quiesce-foreign-session")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateRunning, foreignSession.State)
}
