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

	// @covers AC-STOP-FENCE-004
	// An exact agentctl receipt only advances the durable operation to
	// incomplete. The executor row remains running, so it cannot claim stopped.
	agentctlCutoff := time.Now().UTC()
	incompleteReceipt, err := repo.ConsumeCoordinatorStopFenceReceipt(ctx, "stop-op-a", models.CoordinatorStopFenceReceipt{
		ExecutionID: "execution-a", AgentctlGeneration: 1, AdmissionClosedAt: agentctlCutoff, ManagedProcessesDrained: false,
	})
	require.NoError(t, err)
	require.Equal(t, models.CoordinatorStopOperationStatusIncomplete, incompleteReceipt.Status)
	require.Equal(t, "managed_processes_not_drained", incompleteReceipt.ReasonCode)
	require.Equal(t, models.CoordinatorStopProofScopeAgentctlFence, incompleteReceipt.ProofScope)
	require.Equal(t, agentctlCutoff, incompleteReceipt.AdmissionCutoff, "the durable receipt must contain agentctl's observed admission cutoff")

	// @covers AC-STOP-FENCE-002
	late := &models.ExecutorRunning{
		ID: "session-stop", SessionID: "session-stop", TaskID: "task-stop", ExecutorID: "executor",
		Runtime: agentruntime.RuntimeStandalone, AgentExecutionID: "execution-a", AgentctlGeneration: 1, Status: models.ExecutorRunningStatusReady,
	}
	require.Error(t, repo.UpsertExecutorRunning(ctx, late), "the fenced agentctl incarnation must not re-register")
	require.NoError(t, repo.UpdateTaskSessionState(ctx, "session-stop", models.TaskSessionStateStarting, ""), "an explicit restart reopens the session before registering a new generation")
	require.NoError(t, repo.UpsertExecutorRunning(ctx, &models.ExecutorRunning{
		ID: "session-stop", SessionID: "session-stop", TaskID: "task-stop", ExecutorID: "executor",
		Runtime: agentruntime.RuntimeStandalone, AgentExecutionID: "execution-a", AgentctlGeneration: 2, Status: models.ExecutorRunningStatusStarting,
	}), "a distinct agentctl generation remains available to an explicit restart path")
	_, err = repo.ConsumeCoordinatorStopFenceReceipt(ctx, "stop-op-a", models.CoordinatorStopFenceReceipt{
		ExecutionID: "execution-a", AgentctlGeneration: 1, AdmissionClosedAt: time.Now().UTC(), ManagedProcessesDrained: true,
	})
	require.ErrorIs(t, err, models.ErrExecutionRotated, "a receipt for the old generation cannot mutate a replacement row")

	session, err := repo.GetTaskSession(ctx, "session-stop")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateStarting, session.State)
	turn, err := repo.GetTurn(ctx, "turn-stop")
	require.NoError(t, err)
	require.NotNil(t, turn.CompletedAt)

	repeated, created, err := repo.CaptureCoordinatorStopOperation(ctx, models.CoordinatorStopOperation{
		ID: "stop-op-a", TaskID: "task-stop", SessionID: "session-stop", TurnID: "turn-stop",
		ExecutionID: "execution-a", AgentctlGeneration: 1, ExecutorStatus: models.ExecutorRunningStatusRunning, ExecutorUpdatedAt: running.UpdatedAt,
	})
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, agentctlCutoff, repeated.AdmissionCutoff)

	// @covers AC-STOP-FENCE-004, AC-STOP-FENCE-005
	incomplete, changed, err := repo.MarkCoordinatorStopOperationIncomplete(ctx, "stop-op-a", "execution-a", 1, "agentctl_unreachable")
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, models.CoordinatorStopOperationStatusIncomplete, incomplete.Status)
	require.Equal(t, "managed_processes_not_drained", incomplete.ReasonCode)

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

// @covers AC-STOP-FENCE-005, AC-STOP-FENCE-006
func TestCaptureCoordinatorStopOperationBindsCallerRequestAtomically(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForSessionTests(t)
	now := time.Now().UTC()
	require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: "task-stop-request", Title: "stop", CreatedAt: now, UpdatedAt: now}))
	require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: "parent-stop-request", Title: "parent", CreatedAt: now, UpdatedAt: now}))
	require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{ID: "session-stop-request", TaskID: "task-stop-request", State: models.TaskSessionStateRunning, StartedAt: now, UpdatedAt: now}))
	require.NoError(t, repo.CreateTurn(ctx, &models.Turn{ID: "turn-stop-request", TaskID: "task-stop-request", TaskSessionID: "session-stop-request", StartedAt: now, CreatedAt: now, UpdatedAt: now}))
	require.NoError(t, repo.UpsertExecutorRunning(ctx, &models.ExecutorRunning{ID: "session-stop-request", SessionID: "session-stop-request", TaskID: "task-stop-request", ExecutorID: "executor", Runtime: agentruntime.RuntimeStandalone, AgentExecutionID: "execution-stop-request", AgentctlGeneration: 1, Status: models.ExecutorRunningStatusRunning}))
	_, _, err := repo.CaptureCoordinatorStopRequest(ctx, models.CoordinatorStopRequest{TaskID: "task-stop-request", ParentTaskID: "parent-stop-request", OperationID: "caller-stop-request"})
	require.NoError(t, err)
	running, err := repo.GetExecutorRunningBySessionID(ctx, "session-stop-request")
	require.NoError(t, err)

	op, _, err := repo.CaptureCoordinatorStopOperation(ctx, models.CoordinatorStopOperation{
		ID: "exact-stop-request", RequestOperationID: "caller-stop-request", TaskID: "task-stop-request", SessionID: "session-stop-request", TurnID: "turn-stop-request",
		ExecutionID: "execution-stop-request", AgentctlGeneration: 1, ExecutorStatus: running.Status, ExecutorUpdatedAt: running.UpdatedAt,
	})
	require.NoError(t, err)
	require.Equal(t, models.CoordinatorStopOperationStatusFencing, op.Status)
	bound, err := repo.ListCoordinatorStopRequestReceipts(ctx, "task-stop-request", "caller-stop-request", "parent-stop-request")
	require.NoError(t, err)
	require.Len(t, bound, 1, "receipt binding must commit with session and turn settlement")
	require.Equal(t, op.ID, bound[0].ID)
}

// @covers AC-STOP-FENCE-005, AC-STOP-FENCE-006
func TestFenceCoordinatorStopSessionDoesNotBindUnchangedFenceToNewRequest(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForSessionTests(t)
	now := time.Now().UTC()
	require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: "task-old-fence", Title: "stop", CreatedAt: now, UpdatedAt: now}))
	require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: "parent-old-fence", Title: "parent", CreatedAt: now, UpdatedAt: now}))
	require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{ID: "session-old-fence", TaskID: "task-old-fence", State: models.TaskSessionStateCancelled, StartedAt: now, UpdatedAt: now}))
	changed, err := repo.FenceCoordinatorStopSession(ctx, "task-old-fence", "session-old-fence")
	require.NoError(t, err)
	require.False(t, changed)
	_, _, err = repo.CaptureCoordinatorStopRequest(ctx, models.CoordinatorStopRequest{TaskID: "task-old-fence", ParentTaskID: "parent-old-fence", OperationID: "new-stop-key"})
	require.NoError(t, err)

	changed, err = repo.FenceCoordinatorStopSessionForRequest(ctx, "task-old-fence", "session-old-fence", "new-stop-key")
	require.NoError(t, err)
	require.False(t, changed)
	fences, err := repo.ListCoordinatorStopRequestSessionFences(ctx, "task-old-fence", "new-stop-key")
	require.NoError(t, err)
	require.Empty(t, fences, "a no-op request must not claim a launch fence accepted by another operation")
}

// @covers AC-STOP-FENCE-003, AC-STOP-FENCE-004
func TestFinalizeCoordinatorStopOperationRequiresCapturedTerminalExecutor(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForSessionTests(t)
	now := time.Now().UTC()
	require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: "task-finalize", Title: "finalize", CreatedAt: now, UpdatedAt: now}))
	require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{ID: "session-finalize", TaskID: "task-finalize", State: models.TaskSessionStateRunning, StartedAt: now, UpdatedAt: now}))
	require.NoError(t, repo.CreateTurn(ctx, &models.Turn{ID: "turn-finalize", TaskID: "task-finalize", TaskSessionID: "session-finalize", StartedAt: now, CreatedAt: now, UpdatedAt: now}))
	require.NoError(t, repo.UpsertExecutorRunning(ctx, &models.ExecutorRunning{ID: "session-finalize", SessionID: "session-finalize", TaskID: "task-finalize", ExecutorID: "executor", Runtime: agentruntime.RuntimeStandalone, AgentExecutionID: "execution-finalize", AgentctlGeneration: 1, Status: models.ExecutorRunningStatusRunning}))
	running, err := repo.GetExecutorRunningBySessionID(ctx, "session-finalize")
	require.NoError(t, err)
	op, _, err := repo.CaptureCoordinatorStopOperation(ctx, models.CoordinatorStopOperation{ID: "stop-op-finalize", TaskID: "task-finalize", SessionID: "session-finalize", TurnID: "turn-finalize", ExecutionID: "execution-finalize", AgentctlGeneration: 1, ExecutorStatus: running.Status, ExecutorUpdatedAt: running.UpdatedAt})
	require.NoError(t, err)
	_, err = repo.ConsumeCoordinatorStopFenceReceipt(ctx, op.ID, models.CoordinatorStopFenceReceipt{ExecutionID: op.ExecutionID, AgentctlGeneration: op.AgentctlGeneration, AdmissionClosedAt: time.Now().UTC(), ManagedProcessesDrained: true})
	require.NoError(t, err)

	_, err = repo.FinalizeCoordinatorStopOperation(ctx, op.ID, op.ExecutionID, op.AgentctlGeneration)
	require.ErrorIs(t, err, models.ErrExecutionRotated, "a live captured executor cannot be promoted")
	require.NoError(t, repo.DeleteExecutorRunningBySessionID(ctx, op.SessionID))

	_, err = repo.FinalizeCoordinatorStopOperation(ctx, op.ID, op.ExecutionID, op.AgentctlGeneration)
	require.ErrorIs(t, err, models.ErrExecutionRotated, "row absence is not proof that lifecycle stopped the captured process")
	finalized, err := repo.GetCoordinatorStopOperation(ctx, op.ID)
	require.NoError(t, err)
	require.Equal(t, models.CoordinatorStopOperationStatusIncomplete, finalized.Status)
	require.NoError(t, repo.RecordCoordinatorStopLifecycleProof(ctx, op.ID, op.ExecutionID, op.AgentctlGeneration))
	finalized, err = repo.FinalizeCoordinatorStopOperation(ctx, op.ID, op.ExecutionID, op.AgentctlGeneration)
	require.NoError(t, err, "exact lifecycle proof certifies terminal deletion of the captured executor")
	require.Equal(t, models.CoordinatorStopOperationStatusStopped, finalized.Status)
}

func TestCoordinatorStopCannotRecordLifecycleProofWithoutDrainedAgentctlReceipt(t *testing.T) {
	for _, test := range []struct {
		name          string
		unreachable   bool
		processesDone bool
	}{
		{name: "agentctl unreachable", unreachable: true},
		{name: "managed processes not drained"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			repo := newRepoForSessionTests(t)
			now := time.Now().UTC()
			const taskID, sessionID, turnID, executionID = "task-stop-proof", "session-stop-proof", "turn-stop-proof", "execution-stop-proof"
			require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, Title: "proof", CreatedAt: now, UpdatedAt: now}))
			require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{ID: sessionID, TaskID: taskID, State: models.TaskSessionStateRunning, StartedAt: now, UpdatedAt: now}))
			require.NoError(t, repo.CreateTurn(ctx, &models.Turn{ID: turnID, TaskID: taskID, TaskSessionID: sessionID, StartedAt: now, CreatedAt: now, UpdatedAt: now}))
			require.NoError(t, repo.UpsertExecutorRunning(ctx, &models.ExecutorRunning{ID: sessionID, SessionID: sessionID, TaskID: taskID, ExecutorID: "executor", Runtime: agentruntime.RuntimeStandalone, AgentExecutionID: executionID, AgentctlGeneration: 1, Status: models.ExecutorRunningStatusReady}))
			running, err := repo.GetExecutorRunningBySessionID(ctx, sessionID)
			require.NoError(t, err)
			op, _, err := repo.CaptureCoordinatorStopOperation(ctx, models.CoordinatorStopOperation{ID: "stop-op-proof", TaskID: taskID, SessionID: sessionID, TurnID: turnID, ExecutionID: executionID, AgentctlGeneration: 1, ExecutorStatus: running.Status, ExecutorUpdatedAt: running.UpdatedAt})
			require.NoError(t, err)
			if test.unreachable {
				_, _, err = repo.MarkCoordinatorStopOperationIncomplete(ctx, op.ID, executionID, 1, "agentctl_unreachable")
				require.NoError(t, err)
			} else {
				_, err = repo.ConsumeCoordinatorStopFenceReceipt(ctx, op.ID, models.CoordinatorStopFenceReceipt{ExecutionID: executionID, AgentctlGeneration: 1, AdmissionClosedAt: time.Now().UTC(), ManagedProcessesDrained: test.processesDone})
				require.NoError(t, err)
			}
			require.NoError(t, repo.DeleteExecutorRunningBySessionID(ctx, sessionID))

			require.ErrorIs(t, repo.RecordCoordinatorStopLifecycleProof(ctx, op.ID, executionID, 1), models.ErrExecutionRotated)
			_, err = repo.FinalizeCoordinatorStopOperation(ctx, op.ID, executionID, 1)
			require.ErrorIs(t, err, models.ErrExecutionRotated)
		})
	}
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
