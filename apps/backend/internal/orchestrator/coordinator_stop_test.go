package orchestrator

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentRuntime "github.com/kandev/kandev/internal/agent/runtime"
	"github.com/kandev/kandev/internal/agent/runtime/lifecycle"
	"github.com/kandev/kandev/internal/agentruntime"
	orchestratorexec "github.com/kandev/kandev/internal/orchestrator/executor"
	"github.com/kandev/kandev/internal/orchestrator/messagequeue"
	"github.com/kandev/kandev/internal/orchestrator/watcher"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository/sqlite"
	v1 "github.com/kandev/kandev/pkg/api/v1"
	"github.com/stretchr/testify/require"
)

// @covers AC-STOP-FENCE-001, AC-STOP-FENCE-004
func TestStopTaskForCoordinator_FencesCapturedExecutionBeforeGracefulTeardown(t *testing.T) {
	ctx := context.Background()
	repo := setupTestRepo(t)
	const taskID, sessionID, executionID, turnID = "task-fence", "session-fence", "execution-fence", "turn-fence"
	seedTaskAndSession(t, repo, taskID, sessionID, models.TaskSessionStateRunning)
	now := time.Now().UTC()
	require.NoError(t, repo.CreateTurn(ctx, &models.Turn{ID: turnID, TaskID: taskID, TaskSessionID: sessionID, StartedAt: now, CreatedAt: now, UpdatedAt: now}))
	require.NoError(t, repo.UpsertExecutorRunning(ctx, &models.ExecutorRunning{
		ID: sessionID, SessionID: sessionID, TaskID: taskID, ExecutorID: "executor", Runtime: agentruntime.RuntimeStandalone,
		AgentExecutionID: executionID, AgentctlGeneration: 1, Status: models.ExecutorRunningStatusRunning,
	}))
	manager := &mockAgentManager{repoForExecutionLookup: repo}
	var fenceCalls, stopCalls atomic.Int32
	manager.closeExecutionAdmissionFunc = func(_ context.Context, gotExecutionID string, generation uint64) (*agentRuntime.ExecutionFenceReceipt, error) {
		fenceCalls.Add(1)
		require.Equal(t, executionID, gotExecutionID)
		require.Equal(t, uint64(1), generation)
		return &agentRuntime.ExecutionFenceReceipt{ExecutionID: executionID, AgentctlGeneration: 1, AdmissionClosedAt: time.Now().UTC(), ManagedProcessesDrained: true}, nil
	}
	manager.stopAgentWithReasonFunc = func(_ context.Context, gotExecutionID, _ string, force bool) error {
		stopCalls.Add(1)
		require.Equal(t, executionID, gotExecutionID)
		require.False(t, force)
		return nil
	}
	svc := newCoordinatorStopTestService(repo, newMockTaskRepo(), manager)

	result, err := svc.StopTaskForCoordinator(ctx, taskID)

	require.NoError(t, err)
	require.Equal(t, CoordinatorTaskStopStatusIncomplete, result.Status)
	require.Len(t, result.Receipts, 1)
	require.NotEmpty(t, result.Receipts[0].ID)
	session, err := repo.GetTaskSession(ctx, sessionID)
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateCancelled, session.State)
	turn, err := repo.GetTurn(ctx, turnID)
	require.NoError(t, err)
	require.NotNil(t, turn.CompletedAt)

	retry, err := svc.StopTaskForCoordinator(ctx, taskID)
	require.NoError(t, err)
	require.Equal(t, CoordinatorTaskStopStatusIncomplete, retry.Status)
	require.Len(t, retry.Receipts, 1)
	require.Equal(t, result.Receipts[0].ID, retry.Receipts[0].ID)
	require.EqualValues(t, 2, fenceCalls.Load())
	require.EqualValues(t, 2, stopCalls.Load())
}

// @covers AC-STOP-FENCE-005, AC-STOP-FENCE-006
func TestStopTaskForCoordinatorOperation_RetryResumesOnlyBoundReceipt(t *testing.T) {
	ctx := context.Background()
	repo := setupTestRepo(t)
	const taskID, parentID, sessionID, executionID, turnID = "task-request-stop", "parent-request-stop", "session-request-stop", "execution-request-stop", "turn-request-stop"
	require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: parentID, Title: "parent"}))
	seedTaskAndSession(t, repo, taskID, sessionID, models.TaskSessionStateRunning)
	now := time.Now().UTC()
	require.NoError(t, repo.CreateTurn(ctx, &models.Turn{ID: turnID, TaskID: taskID, TaskSessionID: sessionID, StartedAt: now, CreatedAt: now, UpdatedAt: now}))
	require.NoError(t, repo.UpsertExecutorRunning(ctx, &models.ExecutorRunning{
		ID: sessionID, SessionID: sessionID, TaskID: taskID, ExecutorID: "executor", Runtime: agentruntime.RuntimeStandalone,
		AgentExecutionID: executionID, AgentctlGeneration: 1, Status: models.ExecutorRunningStatusRunning,
	}))
	manager := &mockAgentManager{repoForExecutionLookup: repo}
	var stopCalls atomic.Int32
	manager.closeExecutionAdmissionFunc = func(_ context.Context, gotExecutionID string, generation uint64) (*agentRuntime.ExecutionFenceReceipt, error) {
		require.Equal(t, executionID, gotExecutionID)
		require.Equal(t, uint64(1), generation)
		return &agentRuntime.ExecutionFenceReceipt{ExecutionID: executionID, AgentctlGeneration: generation, AdmissionClosedAt: time.Now().UTC(), ManagedProcessesDrained: true}, nil
	}
	manager.stopAgentWithReasonFunc = func(_ context.Context, gotExecutionID, _ string, force bool) error {
		require.Equal(t, executionID, gotExecutionID)
		require.False(t, force)
		stopCalls.Add(1)
		return nil
	}
	svc := newCoordinatorStopTestService(repo, newMockTaskRepo(), manager)

	first, err := svc.StopTaskForCoordinatorOperation(ctx, taskID, parentID, "lost-ack")
	require.NoError(t, err)
	require.Equal(t, CoordinatorTaskStopStatusIncomplete, first.Status)
	require.Len(t, first.Receipts, 1)
	require.Equal(t, models.CoordinatorStopOperationStatusIncomplete, first.Receipts[0].Status)

	retry, err := svc.StopTaskForCoordinatorOperation(ctx, taskID, parentID, "lost-ack")
	require.NoError(t, err)
	require.Equal(t, CoordinatorTaskStopStatusIncomplete, retry.Status)
	require.Len(t, retry.Receipts, 1)
	require.Equal(t, first.Receipts[0].ID, retry.Receipts[0].ID)
	require.EqualValues(t, 2, stopCalls.Load(), "same request retries the captured incarnation")

	require.NoError(t, repo.UpsertExecutorRunning(ctx, &models.ExecutorRunning{
		ID: sessionID, SessionID: sessionID, TaskID: taskID, ExecutorID: "executor", Runtime: agentruntime.RuntimeStandalone,
		AgentExecutionID: "replacement-execution", AgentctlGeneration: 2, Status: models.ExecutorRunningStatusRunning,
		UpdatedAt: now.Add(time.Second),
	}))
	changedExecutionRetry, err := svc.StopTaskForCoordinatorOperation(ctx, taskID, parentID, "lost-ack")
	require.NoError(t, err)
	require.Equal(t, CoordinatorTaskStopStatusIncomplete, changedExecutionRetry.Status)
	require.Len(t, changedExecutionRetry.Receipts, 1)
	require.Equal(t, first.Receipts[0].ID, changedExecutionRetry.Receipts[0].ID)
	require.EqualValues(t, 2, stopCalls.Load(), "retry must never stop the replacement execution")

	lookup, err := svc.GetCoordinatorStopReceipt(ctx, taskID, parentID, "lost-ack")
	require.NoError(t, err)
	require.Len(t, lookup.Receipts, 1)
	require.Equal(t, first.Receipts[0].ID, lookup.Receipts[0].ID)
}

// @covers AC-STOP-FENCE-005
func TestStopTaskForCoordinatorOperation_RetryCapturesSecondLiveSession(t *testing.T) {
	ctx := context.Background()
	repo := setupTestRepo(t)
	const taskID, parentID = "task-retry-second", "parent-retry-second"
	require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: parentID, Title: "parent"}))
	seedStopRetrySession(t, repo, taskID, "first-session", "first-turn", "first-execution")
	manager := &mockAgentManager{repoForExecutionLookup: repo}
	manager.closeExecutionAdmissionFunc = func(_ context.Context, executionID string, generation uint64) (*agentRuntime.ExecutionFenceReceipt, error) {
		return &agentRuntime.ExecutionFenceReceipt{ExecutionID: executionID, AgentctlGeneration: generation, AdmissionClosedAt: time.Now().UTC(), ManagedProcessesDrained: true}, nil
	}
	manager.stopAgentWithReasonFunc = func(context.Context, string, string, bool) error { return nil }
	svc := newCoordinatorStopTestService(repo, newMockTaskRepo(), manager)

	first, err := svc.StopTaskForCoordinatorOperation(ctx, taskID, parentID, "same-request")
	require.NoError(t, err)
	require.Len(t, first.Receipts, 1)
	seedStopRetrySession(t, repo, taskID, "second-session", "second-turn", "second-execution")

	retry, err := svc.StopTaskForCoordinatorOperation(ctx, taskID, parentID, "same-request")
	require.NoError(t, err)
	require.Len(t, retry.Receipts, 2)
	require.Equal(t, models.TaskSessionStateCancelled, mustGetSession(t, repo, "second-session").State)
}

func seedStopRetrySession(t *testing.T, repo *sqlite.Repository, taskID, sessionID, turnID, executionID string) {
	t.Helper()
	ctx := context.Background()
	if task, err := repo.GetTask(ctx, taskID); err != nil || task == nil {
		seedTaskAndSession(t, repo, taskID, sessionID, models.TaskSessionStateRunning)
	} else {
		now := time.Now().UTC()
		require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{ID: sessionID, TaskID: taskID, State: models.TaskSessionStateRunning, StartedAt: now, UpdatedAt: now}))
	}
	now := time.Now().UTC()
	require.NoError(t, repo.CreateTurn(ctx, &models.Turn{ID: turnID, TaskID: taskID, TaskSessionID: sessionID, StartedAt: now, CreatedAt: now, UpdatedAt: now}))
	require.NoError(t, repo.UpsertExecutorRunning(ctx, &models.ExecutorRunning{ID: sessionID, SessionID: sessionID, TaskID: taskID, ExecutorID: "executor", Runtime: agentruntime.RuntimeStandalone, AgentExecutionID: executionID, AgentctlGeneration: 1, Status: models.ExecutorRunningStatusRunning}))
}

// @covers AC-STOP-FENCE-005, AC-STOP-FENCE-006
func TestStopTaskForCoordinatorOperation_NewKeyDoesNotClaimOlderPendingReceipt(t *testing.T) {
	ctx := context.Background()
	repo := setupTestRepo(t)
	const taskID, parentID, sessionID, executionID, turnID = "task-new-stop-key", "parent-new-stop-key", "session-new-stop-key", "execution-new-stop-key", "turn-new-stop-key"
	require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: parentID, Title: "parent"}))
	seedTaskAndSession(t, repo, taskID, sessionID, models.TaskSessionStateRunning)
	now := time.Now().UTC()
	require.NoError(t, repo.CreateTurn(ctx, &models.Turn{ID: turnID, TaskID: taskID, TaskSessionID: sessionID, StartedAt: now, CreatedAt: now, UpdatedAt: now}))
	require.NoError(t, repo.UpsertExecutorRunning(ctx, &models.ExecutorRunning{
		ID: sessionID, SessionID: sessionID, TaskID: taskID, ExecutorID: "executor", Runtime: agentruntime.RuntimeStandalone,
		AgentExecutionID: executionID, AgentctlGeneration: 1, Status: models.ExecutorRunningStatusRunning,
	}))
	manager := &mockAgentManager{repoForExecutionLookup: repo}
	manager.closeExecutionAdmissionFunc = func(_ context.Context, _ string, _ uint64) (*agentRuntime.ExecutionFenceReceipt, error) {
		return &agentRuntime.ExecutionFenceReceipt{ExecutionID: executionID, AgentctlGeneration: 1, AdmissionClosedAt: time.Now().UTC(), ManagedProcessesDrained: true}, nil
	}
	manager.stopAgentWithReasonFunc = func(_ context.Context, _ string, _ string, _ bool) error { return nil }
	svc := newCoordinatorStopTestService(repo, newMockTaskRepo(), manager)

	first, err := svc.StopTaskForCoordinatorOperation(ctx, taskID, parentID, "first-request")
	require.NoError(t, err)
	require.Len(t, first.Receipts, 1)

	second, err := svc.StopTaskForCoordinatorOperation(ctx, taskID, parentID, "second-request")
	require.NoError(t, err)
	require.Equal(t, CoordinatorTaskStopStatusNotRunning, second.Status)
	require.Empty(t, second.Receipts)

	_, err = svc.GetCoordinatorStopReceipt(ctx, taskID, parentID, "second-request")
	require.NoError(t, err)
}

// @covers AC-STOP-FENCE-005, AC-STOP-FENCE-006
func TestStopTaskForCoordinatorOperation_BindsLaunchFenceToExactRequest(t *testing.T) {
	ctx := context.Background()
	repo := setupTestRepo(t)
	const taskID, parentID, sessionID = "task-launch-fence", "parent-launch-fence", "session-launch-fence"
	require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: parentID, Title: "parent"}))
	seedTaskAndSession(t, repo, taskID, sessionID, models.TaskSessionStateStarting)
	task, err := repo.GetTask(ctx, taskID)
	require.NoError(t, err)
	task.ParentID = parentID
	require.NoError(t, repo.UpdateTask(ctx, task))
	svc := newCoordinatorStopTestService(repo, newMockTaskRepo(), &mockAgentManager{repoForExecutionLookup: repo})

	first, err := svc.StopTaskForCoordinatorOperation(ctx, taskID, parentID, "launch-only-first")
	require.NoError(t, err)
	require.Equal(t, CoordinatorTaskStopStatusIncomplete, first.Status)
	require.Empty(t, first.Receipts)
	require.Len(t, first.SessionFences, 1)
	require.Equal(t, sessionID, first.SessionFences[0].SessionID)

	second, err := svc.StopTaskForCoordinatorOperation(ctx, taskID, parentID, "launch-only-second")
	require.NoError(t, err)
	require.Equal(t, CoordinatorTaskStopStatusNotRunning, second.Status)
	require.Empty(t, second.Receipts)
	require.Empty(t, second.SessionFences, "a new request must not inherit the earlier launch-only fence")

	lookup, err := svc.GetCoordinatorStopReceipt(ctx, taskID, parentID, "launch-only-first")
	require.NoError(t, err)
	require.Equal(t, CoordinatorTaskStopStatusIncomplete, lookup.Status)
	require.Len(t, lookup.SessionFences, 1)
	require.Equal(t, sessionID, lookup.SessionFences[0].SessionID)

	secondLookup, err := svc.GetCoordinatorStopReceipt(ctx, taskID, parentID, "launch-only-second")
	require.NoError(t, err)
	require.Equal(t, CoordinatorTaskStopStatusNotRunning, secondLookup.Status)
	require.Empty(t, secondLookup.SessionFences)

	const pendingTaskID, pendingSessionID, pendingOperationID = "task-pending-launch-fence", "session-pending-launch-fence", "pending-launch-request"
	seedTaskAndSession(t, repo, pendingTaskID, pendingSessionID, models.TaskSessionStateStarting)
	pendingTask, err := repo.GetTask(ctx, pendingTaskID)
	require.NoError(t, err)
	pendingTask.ParentID = parentID
	require.NoError(t, repo.UpdateTask(ctx, pendingTask))
	_, _, err = repo.CaptureCoordinatorStopRequest(ctx, models.CoordinatorStopRequest{TaskID: pendingTaskID, ParentTaskID: parentID, OperationID: pendingOperationID})
	require.NoError(t, err)
	changed, err := repo.FenceCoordinatorStopSessionForRequest(ctx, pendingTaskID, pendingSessionID, pendingOperationID)
	require.NoError(t, err)
	require.True(t, changed)
	pendingLookup, err := svc.GetCoordinatorStopReceipt(ctx, pendingTaskID, parentID, pendingOperationID)
	require.NoError(t, err, "lookup must find the atomically linked launch fence before request completion")
	require.Equal(t, CoordinatorTaskStopStatusIncomplete, pendingLookup.Status)
	require.Len(t, pendingLookup.SessionFences, 1)
}

// @covers AC-STOP-FENCE-005, AC-STOP-FENCE-006
func TestStopTaskForCoordinatorOperation_PersistsEmptyCaptureOutcome(t *testing.T) {
	ctx := context.Background()
	repo := setupTestRepo(t)
	const taskID, parentID = "task-empty-stop", "parent-empty-stop"
	require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: parentID, Title: "parent"}))
	require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, Title: "child", ParentID: parentID}))
	svc := newCoordinatorStopTestService(repo, newMockTaskRepo(), &mockAgentManager{})

	first, err := svc.StopTaskForCoordinatorOperation(ctx, taskID, parentID, "empty-capture")
	require.NoError(t, err)
	require.Equal(t, CoordinatorTaskStopStatusNotRunning, first.Status)
	require.Empty(t, first.Receipts)

	retry, err := svc.StopTaskForCoordinatorOperation(ctx, taskID, parentID, "empty-capture")
	require.NoError(t, err)
	require.Equal(t, CoordinatorTaskStopStatusNotRunning, retry.Status)
	require.Empty(t, retry.Receipts)

	lookup, err := svc.GetCoordinatorStopReceipt(ctx, taskID, parentID, "empty-capture")
	require.NoError(t, err)
	require.Equal(t, CoordinatorTaskStopStatusNotRunning, lookup.Status)
}

type coordinatorStopCallOutcome struct {
	result CoordinatorTaskStopResult
	err    error
}

type coordinatorStopRepoHooks struct {
	repoStore
	listActiveFunc   func(context.Context, string) ([]*models.TaskSession, error)
	fenceSessionFunc func(context.Context, string, string) (bool, error)
	getSessionFunc   func(context.Context, string) (*models.TaskSession, error)
	cancelActiveFunc func(context.Context, string, string) (bool, time.Time, error)
	getTaskFunc      func(context.Context, string) (*models.Task, error)
	updateFullRowCAS func(context.Context, *models.TaskSession, models.TaskSessionState) (bool, error)
}

func (r *coordinatorStopRepoHooks) FenceCoordinatorStopSession(ctx context.Context, taskID, sessionID string) (bool, error) {
	if r.fenceSessionFunc != nil {
		return r.fenceSessionFunc(ctx, taskID, sessionID)
	}
	fencer, ok := r.repoStore.(interface {
		FenceCoordinatorStopSession(context.Context, string, string) (bool, error)
	})
	if !ok {
		return false, errors.New("coordinator stop test repository cannot fence a session")
	}
	return fencer.FenceCoordinatorStopSession(ctx, taskID, sessionID)
}

func (r *coordinatorStopRepoHooks) ListCoordinatorStopSessionFences(ctx context.Context, taskID string) ([]models.CoordinatorStopSessionFenceReceipt, error) {
	fencer, ok := r.repoStore.(interface {
		ListCoordinatorStopSessionFences(context.Context, string) ([]models.CoordinatorStopSessionFenceReceipt, error)
	})
	if !ok {
		return nil, errors.New("coordinator stop test repository cannot list session fences")
	}
	return fencer.ListCoordinatorStopSessionFences(ctx, taskID)
}

func (r *coordinatorStopRepoHooks) UpdateTaskSessionIfCurrentState(
	ctx context.Context,
	session *models.TaskSession,
	expected models.TaskSessionState,
) (bool, error) {
	if r.updateFullRowCAS != nil {
		return r.updateFullRowCAS(ctx, session, expected)
	}
	return r.repoStore.UpdateTaskSessionIfCurrentState(ctx, session, expected)
}

func (r *coordinatorStopRepoHooks) ListActiveTaskSessionsByTaskID(
	ctx context.Context,
	taskID string,
) ([]*models.TaskSession, error) {
	if r.listActiveFunc != nil {
		return r.listActiveFunc(ctx, taskID)
	}
	return r.repoStore.ListActiveTaskSessionsByTaskID(ctx, taskID)
}

func (r *coordinatorStopRepoHooks) GetTaskSession(
	ctx context.Context,
	sessionID string,
) (*models.TaskSession, error) {
	if r.getSessionFunc != nil {
		return r.getSessionFunc(ctx, sessionID)
	}
	return r.repoStore.GetTaskSession(ctx, sessionID)
}

func (r *coordinatorStopRepoHooks) CancelActiveTaskSession(
	ctx context.Context,
	sessionID, reason string,
) (bool, time.Time, error) {
	if r.cancelActiveFunc != nil {
		return r.cancelActiveFunc(ctx, sessionID, reason)
	}
	canceller, ok := r.repoStore.(activeTaskSessionCanceller)
	if !ok {
		return false, time.Time{}, errors.New("coordinator stop test repository cannot cancel an active session")
	}
	return canceller.CancelActiveTaskSession(ctx, sessionID, reason)
}

func (r *coordinatorStopRepoHooks) GetTask(ctx context.Context, taskID string) (*models.Task, error) {
	if r.getTaskFunc != nil {
		return r.getTaskFunc(ctx, taskID)
	}
	return r.repoStore.GetTask(ctx, taskID)
}

type coordinatorStopReadyReadKey struct{}

func TestScheduleTaskForSession_CancelledSessionDoesNotOverwriteReview(t *testing.T) {
	ctx := context.Background()
	repo := setupTestRepo(t)
	seedTaskAndSession(t, repo, "task-cancelled", "session-cancelled", models.TaskSessionStateCancelled)
	taskRepo := newMockTaskRepo()
	seedMockTaskState(taskRepo, "task-cancelled", v1.TaskStateReview)
	svc := newCoordinatorStopTestService(repo, taskRepo, &mockAgentManager{})

	err := svc.scheduleTaskForSession(ctx, "task-cancelled", "session-cancelled")

	require.ErrorIs(t, err, orchestratorexec.ErrSessionStateSuperseded)
	state, history := coordinatorStopTaskStateSnapshot(taskRepo, "task-cancelled")
	require.Equal(t, v1.TaskStateReview, state)
	require.Empty(t, history, "cancelled session must not write task SCHEDULING")
}

func TestStopTaskForCoordinator_NotRunningDisarmsTransientRetry(t *testing.T) {
	ctx := context.Background()
	repo := setupTestRepo(t)
	seedTaskAndSession(t, repo, "task-no-execution", "session-no-execution", models.TaskSessionStateRunning)
	taskRepo := newMockTaskRepo()
	seedMockTaskState(taskRepo, "task-no-execution", v1.TaskStateInProgress)
	svc := newCoordinatorStopTestService(repo, taskRepo, &mockAgentManager{
		getExecutionIDForSessionFunc: func(context.Context, string) (string, error) {
			return "", lifecycle.ErrNoExecutionForSession
		},
	})
	cancelled := make(chan struct{}, 1)
	svc.transientRetries.Store("session-no-execution", &transientRetryEntry{
		attempt: 1,
		cancel: func() {
			cancelled <- struct{}{}
		},
	})
	svc.rememberTurnPrompt("session-no-execution", "retry me", "", false, nil)

	result, err := svc.StopTaskForCoordinator(ctx, "task-no-execution")

	require.NoError(t, err)
	require.Equal(t, CoordinatorTaskStopStatusIncomplete, result.Status)
	coordinatorStopAwaitSignal(t, cancelled, "transient retry cancellation")
	_, retryArmed := svc.transientRetries.Load("session-no-execution")
	require.False(t, retryArmed)
	_, promptCached := svc.lastTurnPrompt.Load("session-no-execution")
	require.False(t, promptCached)
	session, err := repo.GetTaskSession(ctx, "session-no-execution")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateCancelled, session.State)
	state, history := coordinatorStopTaskStateSnapshot(taskRepo, "task-no-execution")
	require.Equal(t, v1.TaskStateReview, state)
	require.NotEmpty(t, history)
}

func TestStopManagedInputExecutionTargetsObservedGeneration(t *testing.T) {
	ctx := context.Background()
	repo := setupTestRepo(t)
	seedTaskAndSession(t, repo, "task-managed-input-stop", "session-managed-input-stop", models.TaskSessionStateRunning)
	manager := &mockAgentManager{
		getExecutionIDForSessionFunc: func(context.Context, string) (string, error) {
			return "execution-current", nil
		},
	}
	stopped := make(chan string, 1)
	manager.stopAgentWithReasonFunc = func(_ context.Context, executionID, _ string, _ bool) error {
		stopped <- executionID
		return nil
	}
	svc := newCoordinatorStopTestService(repo, newMockTaskRepo(), manager)

	stoppedExact, err := svc.StopManagedInputExecution(ctx, "task-managed-input-stop", "session-managed-input-stop", "execution-old")
	require.Error(t, err, "a replacement execution must not be stopped through an older input receipt")
	require.False(t, stoppedExact)
	select {
	case executionID := <-stopped:
		t.Fatalf("stopped execution %q after generation mismatch", executionID)
	default:
	}

	stoppedExact, err = svc.StopManagedInputExecution(ctx, "task-managed-input-stop", "session-managed-input-stop", "execution-current")
	require.NoError(t, err)
	require.True(t, stoppedExact)
	select {
	case executionID := <-stopped:
		require.Equal(t, "execution-current", executionID)
	case <-time.After(time.Second):
		t.Fatal("exact execution was not stopped")
	}
}

func TestStopTaskForCoordinator_ReleasesSessionGuardBeforeTeardown(t *testing.T) {
	ctx := context.Background()
	repo := setupTestRepo(t)
	seedTaskAndSession(t, repo, "task-teardown-order", "session-teardown-order", models.TaskSessionStateRunning)

	guardHeldAtTeardown := make(chan bool, 1)
	manager := &mockAgentManager{
		getExecutionIDForSessionFunc: func(context.Context, string) (string, error) {
			return "execution-teardown-order", nil
		},
	}
	taskRepo := newMockTaskRepo()
	seedMockTaskState(taskRepo, "task-teardown-order", v1.TaskStateInProgress)
	svc := newCoordinatorStopTestService(repo, taskRepo, manager)
	manager.stopAgentWithReasonFunc = func(context.Context, string, string, bool) error {
		guardHeldAtTeardown <- svc.isCancelInFlight("session-teardown-order")
		return nil
	}

	result, err := svc.StopTaskForCoordinator(ctx, "task-teardown-order")
	require.NoError(t, err)
	require.Equal(t, CoordinatorTaskStopStatusIncomplete, result.Status)
	select {
	case <-guardHeldAtTeardown:
		t.Fatal("uncaptured execution must not be torn down by execution ID")
	default:
	}
}

func TestStopTaskForCoordinator_FirstForceTeardownIntentWins(t *testing.T) {
	ctx := context.Background()
	repo := setupTestRepo(t)
	seedTaskAndSession(t, repo, "task-teardown-intent", "session-teardown-intent", models.TaskSessionStateRunning)

	stopEntered := make(chan stopAgentCall, 2)
	allowForceStop := make(chan struct{})
	manager := &mockAgentManager{
		getExecutionIDForSessionFunc: func(context.Context, string) (string, error) {
			return "execution-teardown-intent", nil
		},
		stopAgentWithReasonFunc: func(_ context.Context, executionID, reason string, force bool) error {
			stopEntered <- stopAgentCall{ExecutionID: executionID, Reason: reason, Force: force}
			if force {
				<-allowForceStop
			}
			return nil
		},
	}
	taskRepo := newMockTaskRepo()
	seedMockTaskState(taskRepo, "task-teardown-intent", v1.TaskStateInProgress)
	svc := newCoordinatorStopTestService(repo, taskRepo, manager)

	cleanupDone := make(chan struct{})
	go func() {
		svc.cleanupAgentExecution(
			"execution-teardown-intent",
			"task-teardown-intent",
			"session-teardown-intent",
		)
		close(cleanupDone)
	}()
	first := <-stopEntered
	require.True(t, first.Force)

	result, err := svc.StopTaskForCoordinator(ctx, "task-teardown-intent")
	require.NoError(t, err)
	require.Equal(t, CoordinatorTaskStopStatusIncomplete, result.Status)
	close(allowForceStop)
	coordinatorStopAwaitSignal(t, cleanupDone, "force teardown completion")
	select {
	case duplicate := <-stopEntered:
		t.Fatalf("duplicate teardown intent reached runtime: %#v", duplicate)
	case <-time.After(100 * time.Millisecond):
	}
	session, err := repo.GetTaskSession(ctx, "session-teardown-intent")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateCancelled, session.State)
}

func TestHandleAgentFailed_UnavailableSessionStateStillCleansExecution(t *testing.T) {
	ctx := context.Background()
	baseRepo := setupTestRepo(t)
	seedTaskAndSession(t, baseRepo, "task-failure-unavailable", "session-failure-unavailable", models.TaskSessionStateRunning)
	repo := &coordinatorStopRepoHooks{
		repoStore: baseRepo,
		getSessionFunc: func(context.Context, string) (*models.TaskSession, error) {
			return nil, errors.New("session store unavailable")
		},
	}
	stopCalled := make(chan stopAgentCall, 1)
	manager := &mockAgentManager{
		stopAgentWithReasonFunc: func(_ context.Context, executionID, reason string, force bool) error {
			stopCalled <- stopAgentCall{ExecutionID: executionID, Reason: reason, Force: force}
			return nil
		},
	}
	svc := newCoordinatorStopTestService(repo, newMockTaskRepo(), manager)

	svc.handleAgentFailed(ctx, watcher.AgentEventData{
		TaskID:           "task-failure-unavailable",
		SessionID:        "session-failure-unavailable",
		AgentExecutionID: "execution-failure-unavailable",
		ErrorMessage:     "agent failed",
	})

	select {
	case call := <-stopCalled:
		require.Equal(t, "execution-failure-unavailable", call.ExecutionID)
		require.True(t, call.Force)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for execution-safe cleanup")
	}
}

func TestStopTaskForCoordinator_SerializesStaleAgentFailure(t *testing.T) {
	ctx := context.Background()
	repo := setupTestRepo(t)
	seedTaskAndSession(t, repo, "task-failure-race", "session-failure-race", models.TaskSessionStateRunning)
	stopCalls := make(chan stopAgentCall, 2)
	agentManager := &mockAgentManager{stopAgentWithReasonFunc: func(_ context.Context, executionID, reason string, force bool) error {
		stopCalls <- stopAgentCall{ExecutionID: executionID, Reason: reason, Force: force}
		return nil
	}}
	taskRepo := newMockTaskRepo()
	seedMockTaskState(taskRepo, "task-failure-race", v1.TaskStateInProgress)
	svc := newCoordinatorStopTestService(repo, taskRepo, agentManager)
	messages := &mockMessageCreator{}
	svc.messageCreator = messages

	result, err := svc.StopTaskForCoordinator(ctx, "task-failure-race")
	require.NoError(t, err)
	require.Equal(t, CoordinatorTaskStopStatusIncomplete, result.Status)
	svc.handleAgentFailed(ctx, watcher.AgentEventData{
		TaskID: "task-failure-race", SessionID: "session-failure-race",
		AgentExecutionID: "execution-failure-race", ErrorMessage: "agent crashed",
	})
	select {
	case call := <-stopCalls:
		t.Fatalf("stale failure stopped an uncaptured execution: %#v", call)
	default:
	}

	session, err := repo.GetTaskSession(ctx, "session-failure-race")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateCancelled, session.State)
	require.Empty(t, messages.sessionMessages, "stale failure must not emit recovery messages")
	state, history := coordinatorStopTaskStateSnapshot(taskRepo, "task-failure-race")
	require.Equal(t, v1.TaskStateReview, state)
	require.Equal(t, []v1.TaskState{v1.TaskStateReview}, history)
}

func TestStopTaskForCoordinator_ProcessesCandidatesInStableIDOrder(t *testing.T) {
	ctx := context.Background()
	baseRepo := setupTestRepo(t)
	seedTaskAndSession(t, baseRepo, "task-order", "session-c", models.TaskSessionStateRunning)
	coordinatorStopAddSession(t, baseRepo, "task-order", "session-a", models.TaskSessionStateRunning)
	coordinatorStopAddSession(t, baseRepo, "task-order", "session-b", models.TaskSessionStateRunning)

	repo := &coordinatorStopRepoHooks{
		repoStore: baseRepo,
		listActiveFunc: func(context.Context, string) ([]*models.TaskSession, error) {
			return []*models.TaskSession{
				{ID: "session-c", TaskID: "task-order", State: models.TaskSessionStateRunning},
				{ID: "session-a", TaskID: "task-order", State: models.TaskSessionStateRunning},
				{ID: "session-b", TaskID: "task-order", State: models.TaskSessionStateRunning},
			}, nil
		},
	}
	var lookupMu sync.Mutex
	var lookupOrder []string
	agentManager := &mockAgentManager{
		getExecutionIDForSessionFunc: func(_ context.Context, sessionID string) (string, error) {
			lookupMu.Lock()
			lookupOrder = append(lookupOrder, sessionID)
			lookupMu.Unlock()
			return "", lifecycle.ErrNoExecutionForSession
		},
	}
	taskRepo := newMockTaskRepo()
	seedMockTaskState(taskRepo, "task-order", v1.TaskStateInProgress)
	svc := newCoordinatorStopTestService(repo, taskRepo, agentManager)

	result, err := svc.StopTaskForCoordinator(ctx, "task-order")

	require.NoError(t, err)
	require.Equal(t, CoordinatorTaskStopStatusIncomplete, result.Status)
	lookupMu.Lock()
	gotOrder := append([]string(nil), lookupOrder...)
	lookupMu.Unlock()
	require.Empty(t, gotOrder, "uncaptured executions must not be resolved for teardown")
	require.Len(t, result.SessionFences, 3)
}

func TestStopTaskForCoordinator_AcceptedAndAbsentReturnsStopped(t *testing.T) {
	ctx := context.Background()
	repo := setupTestRepo(t)
	seedTaskAndSession(t, repo, "task-mixed", "session-a", models.TaskSessionStateRunning)
	coordinatorStopAddSession(t, repo, "task-mixed", "session-b", models.TaskSessionStateRunning)

	teardownCalled := make(chan struct{}, 1)
	agentManager := &mockAgentManager{
		getExecutionIDForSessionFunc: func(_ context.Context, sessionID string) (string, error) {
			if sessionID == "session-a" {
				return "execution-a", nil
			}
			return "", lifecycle.ErrNoExecutionForSession
		},
		stopAgentWithReasonFunc: func(context.Context, string, string, bool) error {
			teardownCalled <- struct{}{}
			return nil
		},
	}
	taskRepo := newMockTaskRepo()
	seedMockTaskState(taskRepo, "task-mixed", v1.TaskStateInProgress)
	svc := newCoordinatorStopTestService(repo, taskRepo, agentManager)

	result, err := svc.StopTaskForCoordinator(ctx, "task-mixed")

	require.NoError(t, err)
	require.Equal(t, CoordinatorTaskStopStatusIncomplete, result.Status)
	require.Len(t, result.SessionFences, 2)
	select {
	case <-teardownCalled:
		t.Fatal("uncaptured execution must not be torn down by execution ID")
	default:
	}
	accepted, err := repo.GetTaskSession(ctx, "session-a")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateCancelled, accepted.State)
	absent, err := repo.GetTaskSession(ctx, "session-b")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateCancelled, absent.State)
}

func TestStopTaskForCoordinator_CommittedCancellationSurvivesPostWriteReadFailure(t *testing.T) {
	ctx := context.Background()
	baseRepo := setupTestRepo(t)
	seedTaskAndSession(t, baseRepo, "task-post-write-read", "session-post-write-read", models.TaskSessionStateRunning)

	postWriteReadFailure := errors.New("post-cancellation read failed")
	var cancelCommitted atomic.Bool
	var failedRead atomic.Bool
	repo := &coordinatorStopRepoHooks{
		repoStore: baseRepo,
		getSessionFunc: func(readCtx context.Context, sessionID string) (*models.TaskSession, error) {
			if cancelCommitted.Load() && failedRead.CompareAndSwap(false, true) {
				return nil, postWriteReadFailure
			}
			return baseRepo.GetTaskSession(readCtx, sessionID)
		},
		cancelActiveFunc: func(writeCtx context.Context, sessionID, reason string) (bool, time.Time, error) {
			changed, updatedAt, err := baseRepo.CancelActiveTaskSession(writeCtx, sessionID, reason)
			if changed && err == nil {
				cancelCommitted.Store(true)
			}
			return changed, updatedAt, err
		},
	}
	teardownCalled := make(chan struct{}, 1)
	agentManager := &mockAgentManager{
		getExecutionIDForSessionFunc: func(context.Context, string) (string, error) {
			return "execution-post-write-read", nil
		},
		stopAgentWithReasonFunc: func(context.Context, string, string, bool) error {
			teardownCalled <- struct{}{}
			return nil
		},
	}
	taskRepo := newMockTaskRepo()
	seedMockTaskState(taskRepo, "task-post-write-read", v1.TaskStateInProgress)
	svc := newCoordinatorStopTestService(repo, taskRepo, agentManager)

	result, err := svc.StopTaskForCoordinator(ctx, "task-post-write-read")

	require.NoError(t, err)
	require.Equal(t, CoordinatorTaskStopStatusIncomplete, result.Status)
	select {
	case <-teardownCalled:
		t.Fatal("uncaptured execution must not be torn down by execution ID")
	default:
	}
	session, getErr := baseRepo.GetTaskSession(ctx, "session-post-write-read")
	require.NoError(t, getErr)
	require.Equal(t, models.TaskSessionStateCancelled, session.State)
}

func TestStopTaskForCoordinator_DelayedStaleStateWriterCannotResurrectCancelledSession(t *testing.T) {
	ctx := context.Background()
	repo := setupTestRepo(t)
	seedTaskAndSession(t, repo, "task-stale-writer", "session-stale-writer", models.TaskSessionStateRunning)
	staleSession, err := repo.GetTaskSession(ctx, "session-stale-writer")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateRunning, staleSession.State)

	teardownCalled := make(chan struct{}, 1)
	agentManager := &mockAgentManager{
		getExecutionIDForSessionFunc: func(context.Context, string) (string, error) {
			return "execution-stale-writer", nil
		},
		stopAgentWithReasonFunc: func(context.Context, string, string, bool) error {
			teardownCalled <- struct{}{}
			return nil
		},
	}
	taskRepo := newMockTaskRepo()
	seedMockTaskState(taskRepo, "task-stale-writer", v1.TaskStateInProgress)
	svc := newCoordinatorStopTestService(repo, taskRepo, agentManager)

	result, err := svc.StopTaskForCoordinator(ctx, "task-stale-writer")
	require.NoError(t, err)
	require.Equal(t, CoordinatorTaskStopStatusIncomplete, result.Status)
	select {
	case <-teardownCalled:
		t.Fatal("uncaptured execution must not be torn down by execution ID")
	default:
	}

	// Model a delayed event handler that loaded RUNNING before the stop, then
	// attempts to commit an active state after cancellation was accepted.
	svc.updateTaskSessionState(
		ctx,
		"task-stale-writer",
		"session-stale-writer",
		models.TaskSessionStateWaitingForInput,
		"",
		false,
		staleSession,
	)

	finalSession, getErr := repo.GetTaskSession(ctx, "session-stale-writer")
	require.NoError(t, getErr)
	require.Equal(t, models.TaskSessionStateCancelled, finalSession.State)
}

func TestCompleteAndStopSession_DoesNotRestoreStaleRunningStateAfterCancellation(t *testing.T) {
	ctx := context.Background()
	repo := setupTestRepo(t)
	seedTaskAndSession(t, repo, "task-workflow-cleanup", "session-workflow-cleanup", models.TaskSessionStateRunning)

	staleSession, err := repo.GetTaskSession(ctx, "session-workflow-cleanup")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateRunning, staleSession.State)
	changed, _, err := repo.CancelActiveTaskSession(ctx, staleSession.ID, coordinatorMCPStopReason)
	require.NoError(t, err)
	require.True(t, changed)

	taskRepo := newMockTaskRepo()
	seedMockTaskState(taskRepo, "task-workflow-cleanup", v1.TaskStateInProgress)
	svc := newCoordinatorStopTestService(repo, taskRepo, &mockAgentManager{repoForExecutionLookup: repo})

	// Model workflow cleanup resuming after coordinator stop committed. The
	// cleanup owns a stale RUNNING row and must not write it back wholesale.
	svc.completeAndStopSession(ctx, "task-workflow-cleanup", staleSession)

	stored, err := repo.GetTaskSession(ctx, staleSession.ID)
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateCancelled, stored.State)
	require.Equal(t, coordinatorMCPStopReason, stored.ErrorMessage)
}

func TestSetSessionStarting_CoordinatorCancellationWinsAfterCurrentStateRead(t *testing.T) {
	ctx := context.Background()
	baseRepo := setupTestRepo(t)
	seedTaskAndSession(t, baseRepo, "task-start-race", "session-start-race", models.TaskSessionStateRunning)
	stale, err := baseRepo.GetTaskSession(ctx, "session-start-race")
	require.NoError(t, err)
	stale.State = models.TaskSessionStateStarting

	repo := &coordinatorStopRepoHooks{repoStore: baseRepo}
	repo.updateFullRowCAS = func(
		writeCtx context.Context,
		session *models.TaskSession,
		expected models.TaskSessionState,
	) (bool, error) {
		changed, _, cancelErr := baseRepo.CancelActiveTaskSession(
			writeCtx, session.ID, coordinatorMCPStopReason,
		)
		require.NoError(t, cancelErr)
		require.True(t, changed)
		return baseRepo.UpdateTaskSessionIfCurrentState(writeCtx, session, expected)
	}
	svc := newCoordinatorStopTestService(repo, newMockTaskRepo(), &mockAgentManager{})

	err = svc.setSessionStarting(
		ctx, stale.TaskID, stale, models.TaskSessionStateRunning, true,
	)
	require.Error(t, err)
	stored, getErr := baseRepo.GetTaskSession(ctx, stale.ID)
	require.NoError(t, getErr)
	require.Equal(t, models.TaskSessionStateCancelled, stored.State)
	require.Equal(t, coordinatorMCPStopReason, stored.ErrorMessage)
}

func TestStopTaskForCoordinator_FencesEveryCandidateWithoutExecutionIdentity(t *testing.T) {
	ctx := context.Background()
	repo := setupTestRepo(t)
	seedTaskAndSession(t, repo, "task-partial", "session-a", models.TaskSessionStateRunning)
	coordinatorStopAddSession(t, repo, "task-partial", "session-b", models.TaskSessionStateRunning)
	coordinatorStopAddSession(t, repo, "task-partial", "session-c", models.TaskSessionStateRunning)

	lookupFailure := errors.New("lifecycle lookup failed")
	var lookupMu sync.Mutex
	var lookupOrder []string
	teardownCalled := make(chan struct{}, 1)
	agentManager := &mockAgentManager{
		getExecutionIDForSessionFunc: func(_ context.Context, sessionID string) (string, error) {
			lookupMu.Lock()
			lookupOrder = append(lookupOrder, sessionID)
			lookupMu.Unlock()
			switch sessionID {
			case "session-a":
				return "execution-a", nil
			case "session-b":
				return "", lookupFailure
			default:
				return "", lifecycle.ErrNoExecutionForSession
			}
		},
		stopAgentWithReasonFunc: func(context.Context, string, string, bool) error {
			teardownCalled <- struct{}{}
			return nil
		},
	}
	taskRepo := newMockTaskRepo()
	seedMockTaskState(taskRepo, "task-partial", v1.TaskStateInProgress)
	svc := newCoordinatorStopTestService(repo, taskRepo, agentManager)

	result, err := svc.StopTaskForCoordinator(ctx, "task-partial")

	require.NoError(t, err)
	require.Equal(t, CoordinatorTaskStopStatusIncomplete, result.Status)
	require.Len(t, result.SessionFences, 3)
	select {
	case <-teardownCalled:
		t.Fatal("no exact execution identity was captured")
	default:
	}
	lookupMu.Lock()
	gotOrder := append([]string(nil), lookupOrder...)
	lookupMu.Unlock()
	require.Empty(t, gotOrder, "sessions without executor rows must not be stopped by lifecycle lookup")
	state, history := coordinatorStopTaskStateSnapshot(taskRepo, "task-partial")
	require.Equal(t, v1.TaskStateReview, state)
	require.Equal(t, []v1.TaskState{v1.TaskStateReview}, history)
	for _, sessionID := range []string{"session-a", "session-b", "session-c"} {
		stopped, getErr := repo.GetTaskSession(ctx, sessionID)
		require.NoError(t, getErr)
		require.Equal(t, models.TaskSessionStateCancelled, stopped.State)
	}
}

func TestStopTaskForCoordinator_PartialFailureReconcilesAcceptedStops(t *testing.T) {
	ctx := context.Background()
	baseRepo := setupTestRepo(t)
	seedTaskAndSession(t, baseRepo, "task-partial-review", "session-accepted", models.TaskSessionStateRunning)
	acceptedSession, err := baseRepo.GetTaskSession(ctx, "session-accepted")
	require.NoError(t, err)
	repo := &coordinatorStopRepoHooks{
		repoStore: baseRepo,
		listActiveFunc: func(context.Context, string) ([]*models.TaskSession, error) {
			return []*models.TaskSession{acceptedSession, nil}, nil
		},
	}
	teardownCalled := make(chan struct{}, 1)
	manager := &mockAgentManager{
		getExecutionIDForSessionFunc: func(context.Context, string) (string, error) {
			return "execution-accepted", nil
		},
		stopAgentWithReasonFunc: func(context.Context, string, string, bool) error {
			teardownCalled <- struct{}{}
			return nil
		},
	}
	taskRepo := newMockTaskRepo()
	seedMockTaskState(taskRepo, "task-partial-review", v1.TaskStateInProgress)
	svc := newCoordinatorStopTestService(repo, taskRepo, manager)

	result, stopErr := svc.StopTaskForCoordinator(ctx, "task-partial-review")

	require.ErrorContains(t, stopErr, "candidate is nil or has an empty ID")
	require.Empty(t, result.Status)
	select {
	case <-teardownCalled:
		t.Fatal("the accepted session had no exact execution identity")
	default:
	}
	state, history := coordinatorStopTaskStateSnapshot(taskRepo, "task-partial-review")
	require.Equal(t, v1.TaskStateReview, state)
	require.Equal(t, []v1.TaskState{v1.TaskStateReview}, history)
}

func TestStopTaskForCoordinator_TerminalRereadWinsAfterCandidateSnapshot(t *testing.T) {
	ctx := context.Background()
	baseRepo := setupTestRepo(t)
	seedTaskAndSession(t, baseRepo, "task-terminal", "session-terminal", models.TaskSessionStateRunning)

	snapshotReturned := make(chan struct{})
	var snapshotOnce sync.Once
	repo := &coordinatorStopRepoHooks{
		repoStore: baseRepo,
		listActiveFunc: func(context.Context, string) ([]*models.TaskSession, error) {
			snapshotOnce.Do(func() { close(snapshotReturned) })
			return []*models.TaskSession{{
				ID: "session-terminal", TaskID: "task-terminal", State: models.TaskSessionStateRunning,
			}}, nil
		},
	}
	var lookupCalls atomic.Int32
	agentManager := &mockAgentManager{
		getExecutionIDForSessionFunc: func(context.Context, string) (string, error) {
			lookupCalls.Add(1)
			return "execution-terminal", nil
		},
	}
	taskRepo := newMockTaskRepo()
	seedMockTaskState(taskRepo, "task-terminal", v1.TaskStateInProgress)
	svc := newCoordinatorStopTestService(repo, taskRepo, agentManager)

	guard, releaseGuard := svc.acquireCancelInFlightGuard("session-terminal")
	guard.Lock()
	var unlockOnce sync.Once
	unlockGuard := func() {
		unlockOnce.Do(func() {
			guard.Unlock()
			releaseGuard()
		})
	}
	t.Cleanup(unlockGuard)

	stopDone := make(chan coordinatorStopCallOutcome, 1)
	go func() {
		result, err := svc.StopTaskForCoordinator(ctx, "task-terminal")
		stopDone <- coordinatorStopCallOutcome{result: result, err: err}
	}()
	coordinatorStopAwaitSignal(t, snapshotReturned, "active-session snapshot")
	require.NoError(t, baseRepo.UpdateTaskSessionState(
		ctx, "session-terminal", models.TaskSessionStateCompleted, "completed naturally",
	))
	unlockGuard()

	outcome := coordinatorStopAwaitCall(t, stopDone)
	require.NoError(t, outcome.err)
	require.Equal(t, CoordinatorTaskStopStatusNotRunning, outcome.result.Status)
	require.Zero(t, lookupCalls.Load(), "terminal guarded reread must skip lifecycle lookup")
	session, err := baseRepo.GetTaskSession(ctx, "session-terminal")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateCompleted, session.State)
	_, history := coordinatorStopTaskStateSnapshot(taskRepo, "task-terminal")
	require.Empty(t, history)
}

func TestStopTaskForCoordinator_SerializesReadyBeforeQueueDrain(t *testing.T) {
	ctx := context.Background()
	baseRepo := setupTestRepo(t)
	seedTaskAndSession(t, baseRepo, "task-ready", "session-ready", models.TaskSessionStateRunning)

	cancelWriteEntered := make(chan struct{})
	allowCancelWrite := make(chan struct{})
	readyInitialRead := make(chan struct{})
	var cancelWriteOnce sync.Once
	var readyReadOnce sync.Once
	repo := &coordinatorStopRepoHooks{
		repoStore: baseRepo,
		getSessionFunc: func(readCtx context.Context, sessionID string) (*models.TaskSession, error) {
			session, err := baseRepo.GetTaskSession(readCtx, sessionID)
			if readCtx.Value(coordinatorStopReadyReadKey{}) == true {
				readyReadOnce.Do(func() { close(readyInitialRead) })
			}
			return session, err
		},
		fenceSessionFunc: func(writeCtx context.Context, taskID, sessionID string) (bool, error) {
			cancelWriteOnce.Do(func() { close(cancelWriteEntered) })
			<-allowCancelWrite
			return baseRepo.FenceCoordinatorStopSession(writeCtx, taskID, sessionID)
		},
	}
	var releaseCancelOnce sync.Once
	releaseCancelWrite := func() { releaseCancelOnce.Do(func() { close(allowCancelWrite) }) }
	t.Cleanup(releaseCancelWrite)

	teardownCalled := make(chan struct{}, 1)
	agentManager := &mockAgentManager{
		getExecutionIDForSessionFunc: func(context.Context, string) (string, error) {
			return "execution-ready", nil
		},
		stopAgentWithReasonFunc: func(context.Context, string, string, bool) error {
			teardownCalled <- struct{}{}
			return nil
		},
		promptDone: make(chan struct{}),
	}
	taskRepo := newMockTaskRepo()
	seedMockTaskState(taskRepo, "task-ready", v1.TaskStateInProgress)
	svc := newCoordinatorStopTestService(repo, taskRepo, agentManager)
	_, err := svc.messageQueue.QueueMessage(
		ctx,
		"session-ready",
		"task-ready",
		"must remain queued",
		"",
		messagequeue.QueuedByAgent,
		false,
		nil,
	)
	require.NoError(t, err)

	stopDone := make(chan coordinatorStopCallOutcome, 1)
	go func() {
		result, stopErr := svc.StopTaskForCoordinator(ctx, "task-ready")
		stopDone <- coordinatorStopCallOutcome{result: result, err: stopErr}
	}()
	coordinatorStopAwaitSignal(t, cancelWriteEntered, "blocked cancellation write")

	readyDone := make(chan struct{})
	readyCtx := context.WithValue(ctx, coordinatorStopReadyReadKey{}, true)
	go func() {
		svc.handleAgentReady(readyCtx, watcher.AgentEventData{
			TaskID: "task-ready", SessionID: "session-ready", AgentExecutionID: "execution-ready",
		})
		close(readyDone)
	}()
	coordinatorStopAwaitSignal(t, readyInitialRead, "ready event initial session read")
	coordinatorStopWaitForGuardRefs(t, svc, "session-ready", 2)
	select {
	case <-readyDone:
		t.Fatal("ready event completed while coordinator cancellation owned the shared guard")
	default:
	}

	releaseCancelWrite()
	stopOutcome := coordinatorStopAwaitCall(t, stopDone)
	coordinatorStopAwaitSignal(t, readyDone, "ready event guarded reread")
	select {
	case <-teardownCalled:
		t.Fatal("session without an exact execution receipt must not be stopped by ID")
	default:
	}

	require.NoError(t, stopOutcome.err)
	require.Equal(t, CoordinatorTaskStopStatusIncomplete, stopOutcome.result.Status)
	session, err := baseRepo.GetTaskSession(ctx, "session-ready")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateCancelled, session.State)
	require.Equal(t, 1, svc.messageQueue.GetStatus(ctx, "session-ready").Count)
	agentManager.mu.Lock()
	promptCalls := append([]promptCall(nil), agentManager.capturedPromptCalls...)
	agentManager.mu.Unlock()
	require.Empty(t, promptCalls, "ready event must not dispatch replacement work after cancellation")
}
