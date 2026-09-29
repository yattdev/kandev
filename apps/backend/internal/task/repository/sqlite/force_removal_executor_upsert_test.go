package sqlite

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

func TestClaimForceRemovalBlocksExecutorUpsertByStoredSessionOwner(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-executor-upsert-ws", Name: "Force"}))
	for _, taskID := range []string{"force-executor-upsert-held", "force-executor-upsert-foreign"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: "force-executor-upsert-ws", Title: taskID}))
		require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{ID: taskID + "-session", TaskID: taskID}))
	}
	held, err := repo.GetTask(ctx, "force-executor-upsert-held")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: held.ID, WorkspaceID: held.WorkspaceID, TaskGeneration: held.UpdatedAt, AdmissionGeneration: "admission", OperationID: "executor-upsert", RequestDigest: "request", PreviewDigest: "preview"})
	require.NoError(t, err)

	require.ErrorIs(t, repo.UpsertExecutorRunning(ctx, &models.ExecutorRunning{ID: "held-run", SessionID: "force-executor-upsert-held-session", TaskID: "force-executor-upsert-held"}), ErrForceRemovalTaskHeld)
	_, err = repo.GetExecutorRunningBySessionID(ctx, "force-executor-upsert-held-session")
	require.ErrorIs(t, err, models.ErrExecutorRunningNotFound)

	foreign := &models.ExecutorRunning{ID: "foreign-run", SessionID: "force-executor-upsert-foreign-session", TaskID: "force-executor-upsert-held", AgentExecutionID: "foreign-exec"}
	require.NoError(t, repo.UpsertExecutorRunning(ctx, foreign))
	require.Equal(t, "force-executor-upsert-foreign", foreign.TaskID)
	stored, err := repo.GetExecutorRunningBySessionID(ctx, foreign.SessionID)
	require.NoError(t, err)
	require.Equal(t, "force-executor-upsert-foreign", stored.TaskID)
}
