package sqlite

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

func TestClaimForceRemovalBlocksExecutorResumeTokenUpdate(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-executor-ws", Name: "Force"}))
	for _, id := range []string{"force-executor-held", "force-executor-foreign"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: id, WorkspaceID: "force-executor-ws", Title: id}))
		require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{ID: id + "-session", TaskID: id}))
		require.NoError(t, repo.UpsertExecutorRunning(ctx, &models.ExecutorRunning{ID: id + "-run", SessionID: id + "-session", TaskID: id, AgentExecutionID: "exec", ResumeToken: "before"}))
	}
	held, err := repo.GetTask(ctx, "force-executor-held")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: held.ID, WorkspaceID: held.WorkspaceID, TaskGeneration: held.UpdatedAt, AdmissionGeneration: "admission", OperationID: "executor-resume", RequestDigest: "request", PreviewDigest: "preview"})
	require.NoError(t, err)
	require.ErrorIs(t, repo.UpdateResumeToken(ctx, "force-executor-held-session", "exec", "blocked", ""), ErrForceRemovalTaskHeld)
	got, err := repo.GetExecutorRunningBySessionID(ctx, "force-executor-held-session")
	require.NoError(t, err)
	require.Equal(t, "before", got.ResumeToken)
	require.NoError(t, repo.UpdateResumeToken(ctx, "force-executor-foreign-session", "exec", "allowed", ""))
	require.ErrorIs(t, repo.UpdateResumeToken(ctx, "force-executor-foreign-session", "stale", "nope", ""), models.ErrExecutionRotated)
}
