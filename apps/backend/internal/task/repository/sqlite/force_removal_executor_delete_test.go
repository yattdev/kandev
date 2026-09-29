package sqlite

import (
	"context"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestClaimForceRemovalBlocksExecutorCurrentDelete(t *testing.T) {
	ctx := context.Background()
	r := newRepoForHealTests(t)
	require.NoError(t, r.CreateWorkspace(ctx, &models.Workspace{ID: "force-exec-del-ws", Name: "Force"}))
	for _, id := range []string{"held", "foreign"} {
		require.NoError(t, r.CreateTask(ctx, &models.Task{ID: id, WorkspaceID: "force-exec-del-ws", Title: id}))
		require.NoError(t, r.CreateTaskSession(ctx, &models.TaskSession{ID: id + "s", TaskID: id}))
		require.NoError(t, r.UpsertExecutorRunning(ctx, &models.ExecutorRunning{ID: id + "r", SessionID: id + "s", TaskID: id, AgentExecutionID: "e"}))
	}
	task, e := r.GetTask(ctx, "held")
	require.NoError(t, e)
	_, _, e = r.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: task.ID, WorkspaceID: task.WorkspaceID, TaskGeneration: task.UpdatedAt, AdmissionGeneration: "a", OperationID: "o", RequestDigest: "r", PreviewDigest: "p"})
	require.NoError(t, e)
	require.ErrorIs(t, r.DeleteExecutorRunningIfCurrent(ctx, "helds", "e", time.Time{}), ErrForceRemovalTaskHeld)
	require.NoError(t, r.DeleteExecutorRunningIfCurrent(ctx, "foreigns", "e", time.Time{}))
	require.ErrorIs(t, r.DeleteExecutorRunningIfCurrent(ctx, "foreigns", "e", time.Time{}), models.ErrExecutorRunningNotFound)
}
