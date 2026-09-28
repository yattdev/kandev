package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kandev/kandev/internal/task/models"
)

func TestClaimForceRemovalBlocksFullSessionSnapshotCAS(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-snapshot-ws", Name: "Force"}))
	for _, taskID := range []string{"force-snapshot-held", "force-snapshot-foreign"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: "force-snapshot-ws", Title: taskID}))
		require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{
			ID: taskID + "-session", TaskID: taskID, State: models.TaskSessionStateCreated,
			Metadata: map[string]interface{}{"owner": taskID + "-before"},
		}))
	}

	heldTask, err := repo.GetTask(ctx, "force-snapshot-held")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{
		TaskID: heldTask.ID, WorkspaceID: heldTask.WorkspaceID, TaskGeneration: heldTask.UpdatedAt,
		AdmissionGeneration: "admission", OperationID: "snapshot", RequestDigest: "request", PreviewDigest: "preview",
	})
	require.NoError(t, err)

	heldSession, err := repo.GetTaskSession(ctx, "force-snapshot-held-session")
	require.NoError(t, err)
	heldUpdatedAt := heldSession.UpdatedAt
	heldSession.AgentProfileID = "held-after"
	changed, err := repo.UpdateTaskSessionIfCurrentSnapshot(ctx, heldSession, models.TaskSessionStateCreated, heldUpdatedAt, map[string]interface{}{"owner": "held-after"})
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	require.False(t, changed)
	heldSession, err = repo.GetTaskSession(ctx, "force-snapshot-held-session")
	require.NoError(t, err)
	require.Empty(t, heldSession.AgentProfileID)
	require.Equal(t, "force-snapshot-held-before", heldSession.Metadata["owner"])

	foreignSession, err := repo.GetTaskSession(ctx, "force-snapshot-foreign-session")
	require.NoError(t, err)
	foreignUpdatedAt := foreignSession.UpdatedAt
	foreignSession.AgentProfileID = "foreign-after"
	changed, err = repo.UpdateTaskSessionIfCurrentSnapshot(ctx, foreignSession, models.TaskSessionStateCreated, foreignUpdatedAt, map[string]interface{}{"owner": "foreign-after"})
	require.NoError(t, err)
	require.True(t, changed)
	foreignSession.AgentProfileID = "stale-after"
	changed, err = repo.UpdateTaskSessionIfCurrentSnapshot(ctx, foreignSession, models.TaskSessionStateCreated, foreignUpdatedAt, map[string]interface{}{"owner": "stale-after"})
	require.NoError(t, err)
	require.False(t, changed)
	storedForeign, err := repo.GetTaskSession(ctx, "force-snapshot-foreign-session")
	require.NoError(t, err)
	require.Equal(t, "foreign-after", storedForeign.AgentProfileID)
	require.Equal(t, "foreign-after", storedForeign.Metadata["owner"])
}
