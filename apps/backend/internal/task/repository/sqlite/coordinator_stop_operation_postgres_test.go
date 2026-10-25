package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/agentruntime"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/testutil"
	"github.com/stretchr/testify/require"
)

// @covers AC-STOP-FENCE-003
func TestPostgresCaptureCoordinatorStopOperationRejectsExecutorRotation(t *testing.T) {
	db := testutil.OpenIsolatedPostgres(t, testutil.PostgresDSNFromEnv(t))
	repo, err := NewWithDB(db, db, nil)
	require.NoError(t, err)
	ctx := context.Background()
	now := time.Now().UTC()
	require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: "task-stop-pg", Title: "stop", CreatedAt: now, UpdatedAt: now}))
	require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{
		ID: "session-stop-pg", TaskID: "task-stop-pg", State: models.TaskSessionStateRunning,
		QueueIncarnationID: "incarnation-pg", StartedAt: now, UpdatedAt: now,
	}))
	require.NoError(t, repo.CreateTurn(ctx, &models.Turn{ID: "turn-stop-pg", TaskID: "task-stop-pg", TaskSessionID: "session-stop-pg", StartedAt: now, CreatedAt: now, UpdatedAt: now}))
	require.NoError(t, repo.UpsertExecutorRunning(ctx, &models.ExecutorRunning{
		ID: "session-stop-pg", SessionID: "session-stop-pg", TaskID: "task-stop-pg", ExecutorID: "executor",
		Runtime: agentruntime.RuntimeStandalone, AgentExecutionID: "execution-successor", AgentctlGeneration: 2, Status: models.ExecutorRunningStatusRunning,
	}))
	running, err := repo.GetExecutorRunningBySessionID(ctx, "session-stop-pg")
	require.NoError(t, err)

	_, _, err = repo.CaptureCoordinatorStopOperation(ctx, models.CoordinatorStopOperation{
		ID: "stop-op-pg", TaskID: "task-stop-pg", SessionID: "session-stop-pg", TurnID: "turn-stop-pg",
		ExecutionID: "execution-old", AgentctlGeneration: 1, ExecutorStatus: models.ExecutorRunningStatusRunning, ExecutorUpdatedAt: running.UpdatedAt,
	})
	require.ErrorIs(t, err, models.ErrExecutionRotated)

	session, err := repo.GetTaskSession(ctx, "session-stop-pg")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateRunning, session.State)
}

// @covers AC-STOP-FENCE-004
func TestPostgresFinalizeCoordinatorStopRequiresLifecycleProof(t *testing.T) {
	db := testutil.OpenIsolatedPostgres(t, testutil.PostgresDSNFromEnv(t))
	repo, err := NewWithDB(db, db, nil)
	require.NoError(t, err)
	ctx := context.Background()
	now := time.Now().UTC()
	const taskID, sessionID, turnID, executionID, operationID = "task-stop-proof-pg", "session-stop-proof-pg", "turn-stop-proof-pg", "execution-stop-proof-pg", "stop-op-proof-pg"
	require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, Title: "stop proof", CreatedAt: now, UpdatedAt: now}))
	require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{ID: sessionID, TaskID: taskID, State: models.TaskSessionStateRunning, StartedAt: now, UpdatedAt: now}))
	require.NoError(t, repo.CreateTurn(ctx, &models.Turn{ID: turnID, TaskID: taskID, TaskSessionID: sessionID, StartedAt: now, CreatedAt: now, UpdatedAt: now}))
	require.NoError(t, repo.UpsertExecutorRunning(ctx, &models.ExecutorRunning{ID: sessionID, SessionID: sessionID, TaskID: taskID, ExecutorID: "executor", Runtime: agentruntime.RuntimeStandalone, AgentExecutionID: executionID, AgentctlGeneration: 1, Status: models.ExecutorRunningStatusReady}))
	running, err := repo.GetExecutorRunningBySessionID(ctx, sessionID)
	require.NoError(t, err)
	_, _, err = repo.CaptureCoordinatorStopOperation(ctx, models.CoordinatorStopOperation{ID: operationID, TaskID: taskID, SessionID: sessionID, TurnID: turnID, ExecutionID: executionID, AgentctlGeneration: 1, ExecutorStatus: running.Status, ExecutorUpdatedAt: running.UpdatedAt})
	require.NoError(t, err)
	_, err = repo.ConsumeCoordinatorStopFenceReceipt(ctx, operationID, models.CoordinatorStopFenceReceipt{ExecutionID: executionID, AgentctlGeneration: 1, AdmissionClosedAt: time.Now().UTC(), ManagedProcessesDrained: true})
	require.NoError(t, err)
	require.NoError(t, repo.DeleteExecutorRunningBySessionID(ctx, sessionID))

	_, err = repo.FinalizeCoordinatorStopOperation(ctx, operationID, executionID, 1)
	require.ErrorIs(t, err, models.ErrExecutionRotated)
	require.NoError(t, repo.RecordCoordinatorStopLifecycleProof(ctx, operationID, executionID, 1))
	finalized, err := repo.FinalizeCoordinatorStopOperation(ctx, operationID, executionID, 1)
	require.NoError(t, err)
	require.Equal(t, models.CoordinatorStopOperationStatusStopped, finalized.Status)
}
