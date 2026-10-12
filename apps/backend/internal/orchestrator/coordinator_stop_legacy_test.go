package orchestrator

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	agentRuntime "github.com/kandev/kandev/internal/agent/runtime"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository/sqlite"
	v1 "github.com/kandev/kandev/pkg/api/v1"
	"github.com/stretchr/testify/require"
)

// @covers AC-STOP-FENCE-003, AC-STOP-FENCE-005, AC-STOP-FENCE-007
func TestCoordinatorStopLegacyCandidateRefusesSuccessorCapture(t *testing.T) {
	for _, test := range []struct {
		name, missing string
	}{
		{"all runtime identity missing", "turn_id = '', execution_id = '', agentctl_generation = 0"},
		{"runtime identity missing without executor", "turn_id = '', execution_id = '', agentctl_generation = 0"},
		{"turn missing", "turn_id = ''"},
		{"execution missing", "execution_id = ''"},
		{"generation missing", "agentctl_generation = 0"},
		{"launch identity missing", "queue_incarnation_id = ''"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			repo, svc, stops := newLegacyStopFixture(t)
			request := models.CoordinatorStopRequest{TaskID: "legacy-task", ParentTaskID: "legacy-parent", OperationID: "legacy-request"}
			_, _, _, err := repo.CaptureCoordinatorStopRequestWithCandidates(ctx, request)
			require.NoError(t, err)
			_, err = repo.DB().ExecContext(ctx, "UPDATE task_stop_request_candidates SET "+test.missing+" WHERE task_id = ? AND operation_id = ?", request.TaskID, request.OperationID)
			require.NoError(t, err)
			if test.name != "execution missing" {
				installLegacyStopSuccessorTurn(t, repo)
			}
			if test.name != "turn missing" {
				running, readErr := repo.GetExecutorRunningBySessionID(ctx, "legacy-session")
				require.NoError(t, readErr)
				running.AgentExecutionID, running.AgentctlGeneration = "successor-execution", 2
				require.NoError(t, repo.UpsertExecutorRunning(ctx, running))
			}
			beforeSession := mustGetSession(t, repo, "legacy-session")
			if test.name == "generation missing" || test.name == "launch identity missing" {
				beforeSession.QueueIncarnationID = "successor-queue"
				require.NoError(t, repo.UpdateTaskSession(ctx, beforeSession))
				beforeSession = mustGetSession(t, repo, "legacy-session")
			}
			if test.name == "runtime identity missing without executor" {
				require.NoError(t, repo.DeleteExecutorRunningBySessionID(ctx, "legacy-session"))
				beforeSession = mustGetSession(t, repo, "legacy-session")
			}
			beforeExecutor, err := repo.GetExecutorRunningBySessionID(ctx, "legacy-session")
			if test.name == "runtime identity missing without executor" {
				require.ErrorIs(t, err, models.ErrExecutorRunningNotFound)
			} else {
				require.NoError(t, err)
			}
			beforeTurn, err := repo.GetActiveTurnBySessionID(ctx, "legacy-session")
			require.NoError(t, err)
			_, beforeCandidates, _, err := repo.CaptureCoordinatorStopRequestWithCandidates(ctx, request)
			require.NoError(t, err)
			for range 2 {
				result, stopErr := svc.StopTaskForCoordinatorOperation(ctx, request.TaskID, request.ParentTaskID, request.OperationID)
				require.NoError(t, stopErr)
				require.Equal(t, CoordinatorTaskStopStatusIncomplete, result.Status)
				require.Empty(t, result.Receipts)
				require.Empty(t, result.SessionFences)
			}
			require.Zero(t, stops.Load(), "legacy replay cannot stop a successor")
			require.Equal(t, beforeSession, mustGetSession(t, repo, "legacy-session"))
			afterExecutor, err := repo.GetExecutorRunningBySessionID(ctx, "legacy-session")
			if test.name == "runtime identity missing without executor" {
				require.ErrorIs(t, err, models.ErrExecutorRunningNotFound)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, beforeExecutor, afterExecutor)
			afterTurn, err := repo.GetActiveTurnBySessionID(ctx, "legacy-session")
			require.NoError(t, err)
			require.Equal(t, beforeTurn, afterTurn)
			_, afterCandidates, _, err := repo.CaptureCoordinatorStopRequestWithCandidates(ctx, request)
			require.NoError(t, err)
			require.Equal(t, beforeCandidates, afterCandidates, "retry must not manufacture immutable identity")
			lookup, err := svc.GetCoordinatorStopReceipt(ctx, request.TaskID, request.ParentTaskID, request.OperationID)
			require.NoError(t, err)
			require.Equal(t, CoordinatorTaskStopStatusIncomplete, lookup.Status)
		})
	}
}

// @covers AC-STOP-FENCE-004, AC-STOP-FENCE-007
func TestCoordinatorStopCapturedMissingExecutorNeverFallsBackToLaunchFence(t *testing.T) {
	ctx := context.Background()
	repo, svc, stops := newLegacyStopFixture(t)
	request := models.CoordinatorStopRequest{TaskID: "legacy-task", ParentTaskID: "legacy-parent", OperationID: "missing-executor"}
	_, _, _, err := repo.CaptureCoordinatorStopRequestWithCandidates(ctx, request)
	require.NoError(t, err)
	require.NoError(t, repo.DeleteExecutorRunningBySessionID(ctx, "legacy-session"))
	before := mustGetSession(t, repo, "legacy-session")
	result, err := svc.StopTaskForCoordinatorOperation(ctx, request.TaskID, request.ParentTaskID, request.OperationID)
	require.NoError(t, err)
	require.Equal(t, CoordinatorTaskStopStatusIncomplete, result.Status)
	require.Empty(t, result.Receipts)
	require.Empty(t, result.SessionFences)
	require.Zero(t, stops.Load())
	require.Equal(t, before, mustGetSession(t, repo, "legacy-session"))
	turn, err := repo.GetTurn(ctx, "legacy-turn")
	require.NoError(t, err)
	require.Nil(t, turn.CompletedAt)
}

// @covers AC-STOP-FENCE-003, AC-STOP-FENCE-004, AC-STOP-FENCE-005
func TestCoordinatorStopExactCandidatePositiveControlCompletes(t *testing.T) {
	ctx := context.Background()
	repo, svc, stops := newLegacyStopFixture(t)
	for range 2 {
		result, err := svc.StopTaskForCoordinatorOperation(ctx, "legacy-task", "legacy-parent", "exact-request")
		require.NoError(t, err)
		require.Equal(t, CoordinatorTaskStopStatusStopped, result.Status)
		require.Len(t, result.Receipts, 1)
		require.Equal(t, "legacy-execution", result.Receipts[0].ExecutionID)
		require.Equal(t, uint64(1), result.Receipts[0].AgentctlGeneration)
		require.Equal(t, "exact_executor_terminal", result.Receipts[0].ProofScope)
	}
	require.EqualValues(t, 1, stops.Load())
	require.Equal(t, models.TaskSessionStateCancelled, mustGetSession(t, repo, "legacy-session").State)
	turn, err := repo.GetTurn(ctx, "legacy-turn")
	require.NoError(t, err)
	require.NotNil(t, turn.CompletedAt)
}

func newLegacyStopFixture(t *testing.T) (*sqlite.Repository, *Service, *atomic.Int32) {
	t.Helper()
	ctx := context.Background()
	repo := setupTestRepo(t)
	require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: "legacy-parent", Title: "parent"}))
	seedStopRetrySession(t, repo, "legacy-task", "legacy-session", "legacy-turn", "legacy-execution")
	stops := &atomic.Int32{}
	manager := &mockAgentManager{repoForExecutionLookup: repo}
	manager.closeExecutionAdmissionFunc = func(_ context.Context, executionID string, generation uint64) (*agentRuntime.ExecutionFenceReceipt, error) {
		return &agentRuntime.ExecutionFenceReceipt{ExecutionID: executionID, AgentctlGeneration: generation, AdmissionClosedAt: time.Now().UTC(), ManagedProcessesDrained: true}, nil
	}
	manager.stopAgentWithReasonFunc = func(ctx context.Context, _ string, _ string, force bool) error {
		require.False(t, force)
		stops.Add(1)
		return repo.DeleteExecutorRunningBySessionID(ctx, "legacy-session")
	}
	taskRepo := newMockTaskRepo()
	seedMockTaskState(taskRepo, "legacy-task", v1.TaskStateInProgress)
	return repo, newCoordinatorStopTestService(repo, taskRepo, manager), stops
}

func installLegacyStopSuccessorTurn(t *testing.T, repo *sqlite.Repository) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	_, err := repo.DB().ExecContext(ctx, "UPDATE task_session_turns SET completed_at = ? WHERE id = ?", now, "legacy-turn")
	require.NoError(t, err)
	require.NoError(t, repo.CreateTurn(ctx, &models.Turn{ID: "successor-turn", TaskID: "legacy-task", TaskSessionID: "legacy-session", StartedAt: now, CreatedAt: now, UpdatedAt: now}))
}
