package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/agentruntime"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

// @covers AC-STOP-FENCE-003
func TestCaptureCoordinatorStopOperationSettlesOnlyCapturedIncarnation(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForSessionTests(t)
	now := time.Now().UTC()
	require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: "task-stop", Title: "stop", CreatedAt: now, UpdatedAt: now}))
	require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{
		ID: "session-stop", TaskID: "task-stop", State: models.TaskSessionStateRunning,
		QueueIncarnationID: "incarnation-a", StartedAt: now, UpdatedAt: now,
	}))
	require.NoError(t, repo.CreateTurn(ctx, &models.Turn{
		ID: "turn-stop", TaskID: "task-stop", TaskSessionID: "session-stop", StartedAt: now, CreatedAt: now, UpdatedAt: now,
	}))
	require.NoError(t, repo.UpsertExecutorRunning(ctx, &models.ExecutorRunning{
		ID: "session-stop", SessionID: "session-stop", TaskID: "task-stop", ExecutorID: "executor",
		Runtime: agentruntime.RuntimeStandalone, AgentExecutionID: "execution-a", AgentctlGeneration: 1, Status: models.ExecutorRunningStatusRunning,
	}))
	running, err := repo.GetExecutorRunningBySessionID(ctx, "session-stop")
	require.NoError(t, err)

	op, created, err := repo.CaptureCoordinatorStopOperation(ctx, models.CoordinatorStopOperation{
		ID: "stop-op-a", TaskID: "task-stop", SessionID: "session-stop", TurnID: "turn-stop",
		ExecutionID: "execution-a", AgentctlGeneration: 1, ExecutorStatus: models.ExecutorRunningStatusRunning, ExecutorUpdatedAt: running.UpdatedAt,
	})
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, models.CoordinatorStopOperationStatusFencing, op.Status)
	require.Equal(t, models.CoordinatorStopProofScopePending, op.ProofScope)

	// @covers AC-STOP-FENCE-002
	late := &models.ExecutorRunning{
		ID: "session-stop", SessionID: "session-stop", TaskID: "task-stop", ExecutorID: "executor",
		Runtime: agentruntime.RuntimeStandalone, AgentExecutionID: "execution-a", AgentctlGeneration: 1, Status: models.ExecutorRunningStatusReady,
	}
	require.Error(t, repo.UpsertExecutorRunning(ctx, late), "the fenced agentctl incarnation must not re-register")
	require.NoError(t, repo.UpsertExecutorRunning(ctx, &models.ExecutorRunning{
		ID: "session-stop", SessionID: "session-stop", TaskID: "task-stop", ExecutorID: "executor",
		Runtime: agentruntime.RuntimeStandalone, AgentExecutionID: "execution-a", AgentctlGeneration: 2, Status: models.ExecutorRunningStatusStarting,
	}), "a distinct agentctl generation remains available to an explicit restart path")

	session, err := repo.GetTaskSession(ctx, "session-stop")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateCancelled, session.State)
	turn, err := repo.GetTurn(ctx, "turn-stop")
	require.NoError(t, err)
	require.NotNil(t, turn.CompletedAt)

	repeated, created, err := repo.CaptureCoordinatorStopOperation(ctx, models.CoordinatorStopOperation{
		ID: "stop-op-a", TaskID: "task-stop", SessionID: "session-stop", TurnID: "turn-stop",
		ExecutionID: "execution-a", AgentctlGeneration: 1, ExecutorStatus: models.ExecutorRunningStatusRunning, ExecutorUpdatedAt: running.UpdatedAt,
	})
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, op.AdmissionCutoff, repeated.AdmissionCutoff)

	// @covers AC-STOP-FENCE-004, AC-STOP-FENCE-005
	incomplete, changed, err := repo.MarkCoordinatorStopOperationIncomplete(ctx, "stop-op-a", "execution-a", 1, "agentctl_unreachable")
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, models.CoordinatorStopOperationStatusIncomplete, incomplete.Status)
	require.Equal(t, "agentctl_unreachable", incomplete.ReasonCode)

	// A restart/retry reads the durable incomplete receipt rather than treating
	// the cancelled session as proof that the exact process stopped.
	reloaded, err := repo.GetCoordinatorStopOperation(ctx, "stop-op-a")
	require.NoError(t, err)
	require.Equal(t, models.CoordinatorStopOperationStatusIncomplete, reloaded.Status)
	repeatedIncomplete, changed, err := repo.MarkCoordinatorStopOperationIncomplete(ctx, "stop-op-a", "execution-a", 1, "agentctl_unreachable")
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, models.CoordinatorStopOperationStatusIncomplete, repeatedIncomplete.Status)

	_, _, err = repo.MarkCoordinatorStopOperationIncomplete(ctx, "stop-op-a", "execution-a", 2, "agentctl_unreachable")
	require.ErrorIs(t, err, models.ErrExecutionRotated)
}

// @covers AC-STOP-FENCE-003
func TestCaptureCoordinatorStopOperationRejectsReusedExecutionIDWithNewAgentctlGeneration(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForSessionTests(t)
	now := time.Now().UTC()
	require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: "task-rotated", Title: "rotated", CreatedAt: now, UpdatedAt: now}))
	require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{
		ID: "session-rotated", TaskID: "task-rotated", State: models.TaskSessionStateRunning,
		QueueIncarnationID: "incarnation-b", StartedAt: now, UpdatedAt: now,
	}))
	require.NoError(t, repo.CreateTurn(ctx, &models.Turn{ID: "turn-rotated", TaskID: "task-rotated", TaskSessionID: "session-rotated", StartedAt: now, CreatedAt: now, UpdatedAt: now}))
	require.NoError(t, repo.UpsertExecutorRunning(ctx, &models.ExecutorRunning{
		ID: "session-rotated", SessionID: "session-rotated", TaskID: "task-rotated", ExecutorID: "executor",
		Runtime: agentruntime.RuntimeStandalone, AgentExecutionID: "execution-old", AgentctlGeneration: 2, Status: models.ExecutorRunningStatusRunning,
	}))
	running, err := repo.GetExecutorRunningBySessionID(ctx, "session-rotated")
	require.NoError(t, err)

	_, _, err = repo.CaptureCoordinatorStopOperation(ctx, models.CoordinatorStopOperation{
		ID: "stop-op-stale", TaskID: "task-rotated", SessionID: "session-rotated", TurnID: "turn-rotated",
		ExecutionID: "execution-old", AgentctlGeneration: 1, ExecutorStatus: models.ExecutorRunningStatusRunning, ExecutorUpdatedAt: running.UpdatedAt,
	})
	require.ErrorIs(t, err, models.ErrExecutionRotated)

	session, err := repo.GetTaskSession(ctx, "session-rotated")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateRunning, session.State)
	turn, err := repo.GetTurn(ctx, "turn-rotated")
	require.NoError(t, err)
	if turn.CompletedAt != nil {
		t.Fatal("stale stop closed successor turn")
	}
}

// @covers AC-STOP-FENCE-003
func TestCaptureCoordinatorStopOperationRejectsExecutorStatusChangeAfterCapture(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForSessionTests(t)
	now := time.Now().UTC()
	require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: "task-status", Title: "status", CreatedAt: now, UpdatedAt: now}))
	require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{ID: "session-status", TaskID: "task-status", State: models.TaskSessionStateRunning, StartedAt: now, UpdatedAt: now}))
	require.NoError(t, repo.CreateTurn(ctx, &models.Turn{ID: "turn-status", TaskID: "task-status", TaskSessionID: "session-status", StartedAt: now, CreatedAt: now, UpdatedAt: now}))
	require.NoError(t, repo.UpsertExecutorRunning(ctx, &models.ExecutorRunning{ID: "session-status", SessionID: "session-status", TaskID: "task-status", ExecutorID: "executor", Runtime: agentruntime.RuntimeStandalone, AgentExecutionID: "execution-status", AgentctlGeneration: 1, Status: models.ExecutorRunningStatusReady}))
	captured, err := repo.GetExecutorRunningBySessionID(ctx, "session-status")
	require.NoError(t, err)
	require.NoError(t, repo.UpdateExecutorRunningStatus(ctx, "session-status", models.ExecutorRunningStatusRunning))

	_, _, err = repo.CaptureCoordinatorStopOperation(ctx, models.CoordinatorStopOperation{ID: "stop-op-status", TaskID: "task-status", SessionID: "session-status", TurnID: "turn-status", ExecutionID: "execution-status", AgentctlGeneration: 1, ExecutorStatus: models.ExecutorRunningStatusReady, ExecutorUpdatedAt: captured.UpdatedAt})
	require.ErrorIs(t, err, models.ErrExecutionRotated)
	session, err := repo.GetTaskSession(ctx, "session-status")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateRunning, session.State)
}
