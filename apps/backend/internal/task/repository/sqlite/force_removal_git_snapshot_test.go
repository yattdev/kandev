package sqlite

import (
	"context"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestClaimForceRemovalBlocksLiveGitSnapshotUpsert(t *testing.T) {
	ctx := context.Background()
	r := newRepoForHealTests(t)
	require.NoError(t, r.CreateWorkspace(ctx, &models.Workspace{ID: "force-git-ws", Name: "Force"}))
	for _, id := range []string{"held", "foreign"} {
		require.NoError(t, r.CreateTask(ctx, &models.Task{ID: id, WorkspaceID: "force-git-ws", Title: id}))
		require.NoError(t, r.CreateTaskEnvironment(ctx, &models.TaskEnvironment{ID: id + "e", TaskID: id, ExecutorType: "local", Status: models.TaskEnvironmentStatusReady}))
	}
	h, e := r.GetTask(ctx, "held")
	require.NoError(t, e)
	_, _, e = r.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: h.ID, WorkspaceID: h.WorkspaceID, TaskGeneration: h.UpdatedAt, AdmissionGeneration: "a", OperationID: "o", RequestDigest: "r", PreviewDigest: "p"})
	require.NoError(t, e)
	require.ErrorIs(t, r.UpsertLatestLiveGitSnapshot(ctx, &models.GitSnapshot{ID: "heldsnap", TaskEnvironmentID: "helde"}), ErrForceRemovalTaskHeld)
	require.NoError(t, r.UpsertLatestLiveGitSnapshot(ctx, &models.GitSnapshot{ID: "foreignsnap", TaskEnvironmentID: "foreigne"}))
}
