package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kandev/kandev/internal/task/models"
)

func TestClaimForceRemovalBlocksSessionProfileSnapshotUpdate(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-profile-snapshot-ws", Name: "Force"}))
	for _, taskID := range []string{"force-profile-snapshot-held", "force-profile-snapshot-foreign"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: "force-profile-snapshot-ws", Title: taskID}))
		require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{
			ID: taskID + "-session", TaskID: taskID, AgentProfileSnapshot: map[string]interface{}{"model": taskID + "-before"},
		}))
	}

	heldTask, err := repo.GetTask(ctx, "force-profile-snapshot-held")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: heldTask.ID, WorkspaceID: heldTask.WorkspaceID, TaskGeneration: heldTask.UpdatedAt, AdmissionGeneration: "admission", OperationID: "profile-snapshot", RequestDigest: "request", PreviewDigest: "preview"})
	require.NoError(t, err)

	err = repo.UpdateTaskSessionAgentProfileSnapshot(ctx, "force-profile-snapshot-held-session", map[string]interface{}{"model": "held-after"})
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	heldSession, err := repo.GetTaskSession(ctx, "force-profile-snapshot-held-session")
	require.NoError(t, err)
	require.Equal(t, "force-profile-snapshot-held-before", heldSession.AgentProfileSnapshot["model"])

	require.NoError(t, repo.UpdateTaskSessionAgentProfileSnapshot(ctx, "force-profile-snapshot-foreign-session", map[string]interface{}{"model": "foreign-after"}))
	foreignSession, err := repo.GetTaskSession(ctx, "force-profile-snapshot-foreign-session")
	require.NoError(t, err)
	require.Equal(t, "foreign-after", foreignSession.AgentProfileSnapshot["model"])

	err = repo.UpdateTaskSessionAgentProfileSnapshot(ctx, "force-profile-snapshot-missing-session", nil)
	require.ErrorIs(t, err, models.ErrTaskSessionNotFound)
}
