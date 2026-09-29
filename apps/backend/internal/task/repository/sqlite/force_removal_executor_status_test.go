package sqlite

import (
	"context"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestClaimForceRemovalBlocksExecutorStatusUpdate(t *testing.T) {
	ctx := context.Background()
	r := newRepoForHealTests(t)
	require.NoError(t, r.CreateWorkspace(ctx, &models.Workspace{ID: "force-executor-status-ws", Name: "Force"}))
	for _, id := range []string{"force-executor-status-held", "force-executor-status-foreign"} {
		require.NoError(t, r.CreateTask(ctx, &models.Task{ID: id, WorkspaceID: "force-executor-status-ws", Title: id}))
		require.NoError(t, r.CreateTaskSession(ctx, &models.TaskSession{ID: id + "-session", TaskID: id}))
		require.NoError(t, r.UpsertExecutorRunning(ctx, &models.ExecutorRunning{ID: id + "-run", SessionID: id + "-session", TaskID: id, Status: "running"}))
	}
	held, err := r.GetTask(ctx, "force-executor-status-held")
	require.NoError(t, err)
	_, _, err = r.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: held.ID, WorkspaceID: held.WorkspaceID, TaskGeneration: held.UpdatedAt, AdmissionGeneration: "admission", OperationID: "executor-status", RequestDigest: "request", PreviewDigest: "preview"})
	require.NoError(t, err)
	require.ErrorIs(t, r.UpdateExecutorRunningStatus(ctx, "force-executor-status-held-session", "stopped"), ErrForceRemovalTaskHeld)
	got, err := r.GetExecutorRunningBySessionID(ctx, "force-executor-status-held-session")
	require.NoError(t, err)
	require.Equal(t, "running", got.Status)
	require.NoError(t, r.UpdateExecutorRunningStatus(ctx, "force-executor-status-foreign-session", "stopped"))
	require.ErrorIs(t, r.UpdateExecutorRunningStatus(ctx, "missing", "stopped"), models.ErrExecutorRunningNotFound)
}
