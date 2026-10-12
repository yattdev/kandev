package orchestrator

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/agentruntime"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository/sqlite"
	v1 "github.com/kandev/kandev/pkg/api/v1"
	"github.com/stretchr/testify/require"
)

// @covers AC-STOP-FENCE-005, AC-STOP-FENCE-008
func TestCoordinatorStopUnboundIncompleteRequestRetriesRestoredExactExecutor(t *testing.T) {
	for _, startup := range []bool{false, true} {
		t.Run(map[bool]string{false: "caller retry", true: "startup replay"}[startup], func(t *testing.T) {
			ctx := context.Background()
			repo, svc, stops := newLegacyStopFixture(t)
			request := models.CoordinatorStopRequest{TaskID: "legacy-task", ParentTaskID: "legacy-parent", OperationID: "restored-executor"}
			_, candidates, _, err := repo.CaptureCoordinatorStopRequestWithCandidates(ctx, request)
			require.NoError(t, err)
			running, err := repo.GetExecutorRunningBySessionID(ctx, "legacy-session")
			require.NoError(t, err)
			require.NoError(t, repo.DeleteExecutorRunningBySessionID(ctx, "legacy-session"))
			first, err := svc.StopTaskForCoordinatorOperation(ctx, request.TaskID, request.ParentTaskID, request.OperationID)
			require.NoError(t, err)
			require.Equal(t, CoordinatorTaskStopStatusIncomplete, first.Status)
			require.Empty(t, first.Receipts)
			require.Empty(t, first.SessionFences)
			require.NoError(t, repo.UpsertExecutorRunning(ctx, running))
			if startup {
				require.NoError(t, svc.reconcilePendingCoordinatorStopRequestsOnStartup(ctx))
			} else {
				_, err = svc.StopTaskForCoordinatorOperation(ctx, request.TaskID, request.ParentTaskID, request.OperationID)
				require.NoError(t, err)
			}
			lookup, err := svc.GetCoordinatorStopReceipt(ctx, request.TaskID, request.ParentTaskID, request.OperationID)
			require.NoError(t, err)
			require.Equal(t, CoordinatorTaskStopStatusStopped, lookup.Status)
			require.Len(t, lookup.Receipts, 1)
			require.Equal(t, candidates[0].ExecutionID, lookup.Receipts[0].ExecutionID)
			require.Equal(t, candidates[0].AgentctlGeneration, lookup.Receipts[0].AgentctlGeneration)
			require.EqualValues(t, 1, stops.Load())
			_, replayCandidates, _, err := repo.CaptureCoordinatorStopRequestWithCandidates(ctx, request)
			require.NoError(t, err)
			require.Equal(t, candidates, replayCandidates)
		})
	}
}

// @covers AC-STOP-FENCE-002, AC-STOP-FENCE-005, AC-STOP-FENCE-007, AC-STOP-FENCE-008
func TestStopTaskForCoordinatorOperationRejectsRegistrationAfterSnapshot(t *testing.T) {
	for _, withTurn := range []bool{false, true} {
		t.Run(map[bool]string{false: "before turn reservation", true: "reserved launch turn"}[withTurn], func(t *testing.T) {
			assertCoordinatorStopRegistrationAfterSnapshot(t, withTurn)
		})
	}
}

func assertCoordinatorStopRegistrationAfterSnapshot(t *testing.T, withTurn bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	repo := setupTestRepo(t)
	seedTaskAndSession(t, repo, "launch-task", "launch-session", models.TaskSessionStateStarting)
	require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: "launch-parent", Title: "parent"}))
	if withTurn {
		now := time.Now().UTC()
		require.NoError(t, repo.CreateTurn(ctx, &models.Turn{ID: "launch-turn", TaskID: "launch-task", TaskSessionID: "launch-session", StartedAt: now, CreatedAt: now, UpdatedAt: now}))
	}
	barrier := &coordinatorStopCaptureBarrier{Repository: repo, captured: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(func() { barrier.once.Do(func() { close(barrier.release) }) })
	taskRepo := newMockTaskRepo()
	seedMockTaskState(taskRepo, "launch-task", v1.TaskStateInProgress)
	svc := newCoordinatorStopTestService(barrier, taskRepo, &mockAgentManager{repoForExecutionLookup: repo})
	done := make(chan coordinatorStopCallOutcome, 1)
	go func() {
		result, err := svc.StopTaskForCoordinatorOperation(ctx, "launch-task", "launch-parent", "launch-request")
		done <- coordinatorStopCallOutcome{result: result, err: err}
	}()
	select {
	case <-barrier.captured:
	case <-ctx.Done():
		t.Fatal("stop request did not reach the committed capture barrier")
	}
	running := &models.ExecutorRunning{ID: "launch-session", SessionID: "launch-session", TaskID: "launch-task", ExecutorID: "executor", Runtime: agentruntime.RuntimeStandalone, AgentExecutionID: "late-execution", AgentctlGeneration: 1, Status: models.ExecutorRunningStatusRunning}
	registrationErr := repo.UpsertExecutorRunning(ctx, running)
	barrier.once.Do(func() { close(barrier.release) })
	var outcome coordinatorStopCallOutcome
	select {
	case outcome = <-done:
	case <-ctx.Done():
		t.Fatal("stop request did not finish after capture barrier release")
	}
	require.ErrorIs(t, registrationErr, models.ErrExecutionStopFenced)
	require.NoError(t, outcome.err)
	require.Equal(t, CoordinatorTaskStopStatusIncomplete, outcome.result.Status)
	require.Len(t, outcome.result.SessionFences, 1)
	require.Equal(t, models.TaskSessionStateCancelled, mustGetSession(t, repo, "launch-session").State)
	_, err := repo.GetExecutorRunningBySessionID(ctx, "launch-session")
	require.ErrorIs(t, err, models.ErrExecutorRunningNotFound)
	require.NoError(t, repo.UpdateTaskSessionState(ctx, "launch-session", models.TaskSessionStateStarting, ""))
	_, err = svc.StopTaskForCoordinatorOperation(ctx, "launch-task", "launch-parent", "launch-request")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateStarting, mustGetSession(t, repo, "launch-session").State, "retry must not recapture an authorized restart before its registration")
	running.AgentExecutionID, running.AgentctlGeneration = "authorized-successor", 2
	require.NoError(t, repo.UpsertExecutorRunning(ctx, running), "an explicit restart is independent of the completed launch request")
	_, err = svc.StopTaskForCoordinatorOperation(ctx, "launch-task", "launch-parent", "launch-request")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateStarting, mustGetSession(t, repo, "launch-session").State)
	successor, err := repo.GetExecutorRunningBySessionID(ctx, "launch-session")
	require.NoError(t, err)
	require.Equal(t, "authorized-successor", successor.AgentExecutionID)
}

type coordinatorStopCaptureBarrier struct {
	*sqlite.Repository
	captured, release chan struct{}
	once              sync.Once
}

func (r *coordinatorStopCaptureBarrier) CaptureCoordinatorStopRequestWithCandidates(ctx context.Context, request models.CoordinatorStopRequest) (*models.CoordinatorStopRequest, []models.CoordinatorStopRequestCandidate, bool, error) {
	got, candidates, created, err := r.Repository.CaptureCoordinatorStopRequestWithCandidates(ctx, request)
	if err != nil || !created {
		return got, candidates, created, err
	}
	close(r.captured)
	select {
	case <-r.release:
		return got, candidates, created, nil
	case <-ctx.Done():
		return nil, nil, false, ctx.Err()
	}
}
