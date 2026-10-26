package sqlite

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

func TestUpsertExecutorRunningRejectsUnknownGenerationForSameExecution(t *testing.T) {
	repo := newRepoForSessionTests(t)
	assertUpsertExecutorRunningRejectsUnknownGenerationForSameExecution(t, repo)
}

func assertUpsertExecutorRunningRejectsUnknownGenerationForSameExecution(t *testing.T, repo *Repository) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: "generation-task", Title: "generation"}))
	require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{
		ID: "generation-session", TaskID: "generation-task", State: models.TaskSessionStateRunning,
	}))
	running := &models.ExecutorRunning{
		ID: "generation-session", SessionID: "generation-session", TaskID: "generation-task",
		AgentExecutionID: "generation-execution", AgentctlGeneration: 7, Status: models.ExecutorRunningStatusRunning,
	}
	require.NoError(t, repo.UpsertExecutorRunning(ctx, running))

	legacy := *running
	legacy.AgentctlGeneration = 0
	legacy.Status = models.ExecutorRunningStatusReady
	require.ErrorIs(t, repo.UpsertExecutorRunning(ctx, &legacy), models.ErrExecutionRotated)
	stored, err := repo.GetExecutorRunningBySessionID(ctx, running.SessionID)
	require.NoError(t, err)
	require.Equal(t, uint64(7), stored.AgentctlGeneration)
	require.Equal(t, models.ExecutorRunningStatusRunning, stored.Status)

	require.NoError(t, repo.UpsertExecutorRunning(ctx, running), "an exact-generation replay remains valid")
	newExecution := *running
	newExecution.AgentExecutionID = "replacement-execution"
	newExecution.AgentctlGeneration = 0
	require.NoError(t, repo.UpsertExecutorRunning(ctx, &newExecution), "a distinct legacy execution remains independently representable")
}
