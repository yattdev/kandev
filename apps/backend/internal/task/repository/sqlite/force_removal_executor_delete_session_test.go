package sqlite

import (
	"context"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestClaimForceRemovalBlocksExecutorSessionDelete(t *testing.T) {
	ctx := context.Background()
	r := newRepoForHealTests(t)
	require.NoError(t, r.CreateWorkspace(ctx, &models.Workspace{ID: "force-exec-sdel-ws", Name: "Force"}))
	for _, id := range []string{"held", "foreign"} {
		require.NoError(t, r.CreateTask(ctx, &models.Task{ID: id, WorkspaceID: "force-exec-sdel-ws", Title: id}))
		require.NoError(t, r.CreateTaskSession(ctx, &models.TaskSession{ID: id + "s", TaskID: id}))
		require.NoError(t, r.UpsertExecutorRunning(ctx, &models.ExecutorRunning{ID: id + "r", SessionID: id + "s", TaskID: id}))
	}
	h, e := r.GetTask(ctx, "held")
	require.NoError(t, e)
	_, _, e = r.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: h.ID, WorkspaceID: h.WorkspaceID, TaskGeneration: h.UpdatedAt, AdmissionGeneration: "a", OperationID: "o", RequestDigest: "r", PreviewDigest: "p"})
	require.NoError(t, e)
	require.ErrorIs(t, r.DeleteExecutorRunningBySessionID(ctx, "helds"), ErrForceRemovalTaskHeld)
	require.NoError(t, r.DeleteExecutorRunningBySessionID(ctx, "foreigns"))
	require.ErrorIs(t, r.DeleteExecutorRunningBySessionID(ctx, "foreigns"), models.ErrExecutorRunningNotFound)
}
