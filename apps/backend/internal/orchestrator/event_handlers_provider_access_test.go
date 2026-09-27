package orchestrator

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/kandev/kandev/internal/agent/runtime/lifecycle"
	"github.com/kandev/kandev/internal/orchestrator/watcher"
	"github.com/kandev/kandev/internal/task/models"
	sqliterepo "github.com/kandev/kandev/internal/task/repository/sqlite"
)

func TestAgentStoppedRetriesProviderRevocationBeforeTerminalSessionCommit(t *testing.T) {
	ctx := context.Background()
	repo := setupTestRepo(t)
	seedSession(t, repo, "t1", "s1", "step1")
	svc := createTestService(repo, newMockStepGetter(), newMockTaskRepo())
	events := &recordingEventBus{}
	svc.eventBus = events
	revoker := &providerSessionRevokeRecorder{err: errors.New("provider revocation unavailable")}
	svc.SetProviderAccessSessionRevoker(revoker)
	event := watcher.AgentEventData{TaskID: "t1", SessionID: "s1", AgentExecutionID: "exec-1"}

	svc.handleAgentStopped(ctx, event)
	first, err := repo.GetTaskSession(ctx, "s1")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateRunning, first.State)
	require.Equal(t, []string{"s1"}, revoker.calls)
	require.Empty(t, events.events)

	revoker.err = nil
	svc.handleAgentStopped(ctx, event)
	second, err := repo.GetTaskSession(ctx, "s1")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateCancelled, second.State)
	require.Equal(t, []string{"s1", "s1"}, revoker.calls)
	require.Len(t, events.events, 1)
}

func TestBootstrapFailureRetriesProviderRevocationBeforeTerminalCommit(t *testing.T) {
	ctx := context.Background()
	repo := setupTestRepo(t)
	seedSession(t, repo, "t1", "s1", "step1")
	session, err := repo.GetTaskSession(ctx, "s1")
	require.NoError(t, err)
	session.State = models.TaskSessionStateStarting
	require.NoError(t, repo.UpdateTaskSession(ctx, session))
	seedExecutorRunning(t, repo, "s1", "t1", "exec-1")

	svc := createTestService(repo, newMockStepGetter(), newMockTaskRepo())
	svc.messageCreator = newServiceBackedMessageCreator(repo)
	events := &recordingEventBus{}
	svc.eventBus = events
	revokeErr := errors.New("provider revocation unavailable")
	revoker := &providerSessionRevokeRecorder{err: revokeErr}
	svc.SetProviderAccessSessionRevoker(revoker)
	failure := models.LastAgentError{
		Message: "The agent could not start.", OccurredAt: time.Now().UTC(),
		AgentExecutionID: "exec-1", ExecutionID: "exec-1", StampValue: "bootstrap-failure-1",
	}

	changed, state, err := svc.transitionBootstrapFailure(ctx, "t1", "s1", "exec-1",
		models.TaskSessionStateStarting, "", "", failure)
	require.False(t, changed)
	require.Equal(t, models.TaskSessionStateStarting, state)
	require.ErrorIs(t, err, revokeErr)
	stored, err := repo.GetTaskSession(ctx, "s1")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateStarting, stored.State)
	require.Empty(t, events.events)
	messages, err := repo.ListMessages(ctx, "s1")
	require.NoError(t, err)
	require.Empty(t, messages)

	revoker.err = nil
	changed, state, err = svc.transitionBootstrapFailure(ctx, "t1", "s1", "exec-1",
		models.TaskSessionStateStarting, "", "", failure)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, models.TaskSessionStateFailed, state)
	require.Equal(t, []string{"s1", "s1"}, revoker.calls)
	require.Len(t, events.events, 1)
	messages, err = repo.ListMessages(ctx, "s1")
	require.NoError(t, err)
	require.Len(t, messages, 1)
}

func TestRetainedWorkflowDestinationRetriesProviderRevocationBeforeFailure(t *testing.T) {
	ctx := context.Background()
	repo := setupTestRepo(t)
	seedSession(t, repo, "t1", "s1", "step1")
	destination, err := repo.GetTaskSession(ctx, "s1")
	require.NoError(t, err)
	svc := createTestService(repo, newMockStepGetter(), newMockTaskRepo())
	revokeErr := errors.New("provider revocation unavailable")
	revoker := &providerSessionRevokeRecorder{err: revokeErr}
	svc.SetProviderAccessSessionRevoker(revoker)
	cause := errors.New("queue transfer failed")

	err = svc.retainFailedWorkflowDestination(ctx, "t1", destination, cause)
	require.ErrorIs(t, err, revokeErr)
	stored, err := repo.GetTaskSession(ctx, "s1")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateRunning, stored.State)

	revoker.err = nil
	require.NoError(t, svc.retainFailedWorkflowDestination(ctx, "t1", destination, cause))
	stored, err = repo.GetTaskSession(ctx, "s1")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateFailed, stored.State)
	require.Equal(t, []string{"s1", "s1"}, revoker.calls)
}

func TestStaleBootstrapFailureDoesNotRevokeSuccessorProviderToken(t *testing.T) {
	ctx := context.Background()
	repo := setupTestRepo(t)
	seedSession(t, repo, "t1", "s1", "step1")
	session, err := repo.GetTaskSession(ctx, "s1")
	require.NoError(t, err)
	session.State = models.TaskSessionStateStarting
	require.NoError(t, repo.UpdateTaskSession(ctx, session))
	seedExecutorRunning(t, repo, "s1", "t1", "exec-old")
	seedExecutorRunning(t, repo, "s1", "t1", "exec-new")
	svc := createTestService(repo, newMockStepGetter(), newMockTaskRepo())
	revoker := &providerSessionRevokeRecorder{}
	svc.SetProviderAccessSessionRevoker(revoker)

	changed, state, err := svc.transitionBootstrapFailure(ctx, "t1", "s1", "exec-old",
		models.TaskSessionStateStarting, "", "", models.LastAgentError{
			Message: "stale failure", OccurredAt: time.Now().UTC(),
			AgentExecutionID: "exec-old", ExecutionID: "exec-old", StampValue: "stale-failure",
		})
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, models.TaskSessionStateStarting, state)
	require.Empty(t, revoker.calls)
	current, err := repo.GetTaskSession(ctx, "s1")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateStarting, current.State)
	running, err := repo.GetExecutorRunningBySessionID(ctx, "s1")
	require.NoError(t, err)
	require.Equal(t, "exec-new", running.AgentExecutionID)
}

func TestStaleBootstrapAttemptDoesNotRevokeCurrentProviderToken(t *testing.T) {
	ctx := context.Background()
	repo := setupTestRepo(t)
	seedSession(t, repo, "t1", "s1", "step1")
	require.NoError(t, repo.UpdateTaskSessionState(ctx, "s1", models.TaskSessionStateStarting, ""))
	require.NoError(t, repo.SetSessionMetadataKey(ctx, "s1", models.SessionMetaKeyAgentStartAttemptID, "attempt-new"))
	seedExecutorRunning(t, repo, "s1", "t1", "exec-1")
	svc := createTestService(repo, newMockStepGetter(), newMockTaskRepo())
	revoker := &providerSessionRevokeRecorder{}
	svc.SetProviderAccessSessionRevoker(revoker)

	changed, state, err := svc.transitionBootstrapFailure(ctx, "t1", "s1", "exec-1",
		models.TaskSessionStateStarting, "", "attempt-old", models.LastAgentError{
			Message: "stale start", OccurredAt: time.Now().UTC(), AgentExecutionID: "exec-1",
			ExecutionID: "exec-1", StampValue: "stale-attempt",
		})
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, models.TaskSessionStateStarting, state)
	require.Empty(t, revoker.calls)
}

func TestStalePreloadedTerminalStateDoesNotRevokeProviderToken(t *testing.T) {
	ctx := context.Background()
	repo := setupTestRepo(t)
	seedSession(t, repo, "t1", "s1", "step1")
	stale, err := repo.GetTaskSession(ctx, "s1")
	require.NoError(t, err)
	require.NoError(t, repo.UpdateTaskSessionState(ctx, "s1", models.TaskSessionStateStarting, ""))
	svc := createTestService(repo, newMockStepGetter(), newMockTaskRepo())
	svc.messageQueue = nil
	revoker := &providerSessionRevokeRecorder{}
	svc.SetProviderAccessSessionRevoker(revoker)

	_, changed := svc.updateTaskSessionStateWithHook(ctx, "t1", "s1", models.TaskSessionStateCancelled, "", false, nil, stale)
	require.False(t, changed)
	require.Empty(t, revoker.calls)
	current, err := repo.GetTaskSession(ctx, "s1")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateStarting, current.State)
}

func TestStoppedExecutionCannotRevokeRotatedExecutionAtSameState(t *testing.T) {
	ctx := context.Background()
	repo := setupTestRepo(t)
	seedSession(t, repo, "t1", "s1", "step1")
	seedExecutorRunning(t, repo, "s1", "t1", "exec-new")
	svc := createTestService(repo, newMockStepGetter(), newMockTaskRepo())
	svc.messageQueue = nil
	revoker := &providerSessionRevokeRecorder{}
	svc.SetProviderAccessSessionRevoker(revoker)
	staleCtx := context.WithValue(ctx, terminalProviderExecutionKey{}, "exec-old")

	_, changed := svc.updateTaskSessionStateWithHook(staleCtx, "t1", "s1", models.TaskSessionStateCancelled, "", false, nil)
	require.False(t, changed)
	require.Empty(t, revoker.calls)
	current, err := repo.GetTaskSession(ctx, "s1")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateRunning, current.State)
}

func TestOldWorkspaceLaunchCannotRevokeNewExecutionAtSameState(t *testing.T) {
	ctx := context.Background()
	repo := setupTestRepo(t)
	seedSession(t, repo, "t1", "s1", "step1")
	require.NoError(t, repo.UpdateTaskSessionState(ctx, "s1", models.TaskSessionStateStarting, ""))
	// The workspace-only launch began before an executor existed. A later
	// attempt can register a new execution without changing session state.
	oldLaunchCtx := context.WithValue(ctx, terminalProviderNoExecutionKey{}, true)
	seedExecutorRunning(t, repo, "s1", "t1", "exec-new")
	svc := createTestService(repo, newMockStepGetter(), newMockTaskRepo())
	svc.messageQueue = nil
	revoker := &providerSessionRevokeRecorder{}
	svc.SetProviderAccessSessionRevoker(revoker)

	changed := svc.recordSessionLaunchFailure(oldLaunchCtx, "t1", "s1", errors.New("old launch failed"))
	require.False(t, changed)
	require.Empty(t, revoker.calls)
	current, err := repo.GetTaskSession(ctx, "s1")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateStarting, current.State)
	running, err := repo.GetExecutorRunningBySessionID(ctx, "s1")
	require.NoError(t, err)
	require.Equal(t, "exec-new", running.AgentExecutionID)
}

func TestWorkspaceLaunchFailureRevokesOnlyWhileNoExecutionOwnsSession(t *testing.T) {
	ctx := context.Background()
	repo := setupTestRepo(t)
	seedSession(t, repo, "t1", "s1", "step1")
	require.NoError(t, repo.UpdateTaskSessionState(ctx, "s1", models.TaskSessionStateStarting, ""))
	svc := createTestService(repo, newMockStepGetter(), newMockTaskRepo())
	svc.messageQueue = nil
	revoker := &providerSessionRevokeRecorder{}
	svc.SetProviderAccessSessionRevoker(revoker)

	// An unbound callback cannot assert it owns the current attempt.
	require.False(t, svc.recordSessionLaunchFailure(ctx, "t1", "s1", errors.New("unbound failure")))
	require.Empty(t, revoker.calls)
	launchCtx := context.WithValue(ctx, terminalProviderNoExecutionKey{}, true)
	require.True(t, svc.recordSessionLaunchFailure(launchCtx, "t1", "s1", errors.New("workspace failed")))
	require.Equal(t, []string{"s1"}, revoker.calls)
	current, err := repo.GetTaskSession(ctx, "s1")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateFailed, current.State)
}

func TestOldWorkspaceLaunchCannotRevokeNewAttemptBeforeExecutionRegisters(t *testing.T) {
	ctx := context.Background()
	repo := setupTestRepo(t)
	seedSession(t, repo, "t1", "s1", "step1")
	require.NoError(t, repo.UpdateTaskSessionState(ctx, "s1", models.TaskSessionStateStarting, ""))
	oldLaunchCtx := context.WithValue(ctx, terminalProviderNoExecutionKey{}, true)
	require.NoError(t, repo.SetSessionMetadataKey(ctx, "s1", models.SessionMetaKeyAgentStartAttemptID, "attempt-new"))
	svc := createTestService(repo, newMockStepGetter(), newMockTaskRepo())
	svc.messageQueue = nil
	revoker := &providerSessionRevokeRecorder{}
	svc.SetProviderAccessSessionRevoker(revoker)
	require.False(t, svc.recordSessionLaunchFailure(oldLaunchCtx, "t1", "s1", errors.New("old launch failed")))
	require.Empty(t, revoker.calls)
	current, err := repo.GetTaskSession(ctx, "s1")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateStarting, current.State)
}

func TestOldStallCallbackCannotRevokeRotatedExecution(t *testing.T) {
	ctx := context.Background()
	repo := setupTestRepo(t)
	seedSession(t, repo, "t1", "s1", "step1")
	seedExecutorRunning(t, repo, "s1", "t1", "exec-new")
	agentMgr := &mockAgentManager{currentPromptExecutionID: "exec-old"}
	agentMgr.currentPromptGeneration.Store(7)
	svc := createTestServiceWithScheduler(repo, newMockStepGetter(), newMockTaskRepo(), agentMgr)
	svc.turnService = &repoTurnService{repo: repo}
	_, err := svc.turnService.StartTurn(ctx, "s1")
	require.NoError(t, err)
	svc.messageCreator = &mockMessageCreator{}
	revoker := &providerSessionRevokeRecorder{}
	svc.SetProviderAccessSessionRevoker(revoker)
	svc.handleAgentStalled(ctx, lifecycle.AgentStalledPayload{
		TaskID: "t1", SessionID: "s1", AgentExecutionID: "exec-old", PromptGeneration: 7, NeverStarted: true,
	})
	require.Empty(t, revoker.calls)
	current, err := repo.GetTaskSession(ctx, "s1")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateRunning, current.State)
}

func TestCommittedTerminalClaimReconcilesAfterServiceRestart(t *testing.T) {
	ctx := context.Background()
	repo := setupTestRepo(t)
	seedSession(t, repo, "t1", "s1", "step1")
	claimID, claimed, err := repo.ClaimProviderAccessTerminal(ctx, models.TerminalProviderAccessClaim{
		TaskID: "t1", SessionID: "s1", ExpectedState: models.TaskSessionStateRunning,
		TargetState: models.TaskSessionStateFailed, ErrorMessage: "launch failed",
	})
	require.NoError(t, err)
	require.True(t, claimed)
	require.NotEmpty(t, claimID)

	// Construct a fresh service over the durable repository, as after a crash.
	restarted := createTestService(repo, newMockStepGetter(), newMockTaskRepo())
	revoker := &providerSessionRevokeRecorder{err: errors.New("revocation unconfirmed")}
	restarted.SetProviderAccessSessionRevoker(revoker)
	restarted.reconcileSessionsOnStartup(ctx)
	pending, err := repo.ListPendingProviderAccessTerminalClaims(ctx)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	state, err := repo.GetTaskSession(ctx, "s1")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateRunning, state.State)
	changed, _, err := repo.UpdateTaskSessionStateIfCurrent(ctx, "s1", models.TaskSessionStateRunning,
		models.TaskSessionStateCancelled, "another writer")
	require.NoError(t, err)
	require.False(t, changed)

	revoker.err = nil
	restarted.reconcileSessionsOnStartup(ctx)
	pending, err = repo.ListPendingProviderAccessTerminalClaims(ctx)
	require.NoError(t, err)
	require.Empty(t, pending)
	state, err = repo.GetTaskSession(ctx, "s1")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateFailed, state.State)
	require.Equal(t, "launch failed", state.ErrorMessage)
	require.Equal(t, []string{"s1", "s1"}, revoker.calls)
}

type staleTerminalReadRepo struct {
	*sqliterepo.Repository
	staleRead bool
}

func (r *staleTerminalReadRepo) GetTaskSession(ctx context.Context, sessionID string) (*models.TaskSession, error) {
	session, err := r.Repository.GetTaskSession(ctx, sessionID)
	if err == nil && !r.staleRead {
		r.staleRead = true
		if updateErr := r.UpdateTaskSessionState(ctx, sessionID, models.TaskSessionStateStarting, ""); updateErr != nil {
			return nil, updateErr
		}
	}
	return session, err
}

func TestStrictTerminalStateChangedAfterReadDoesNotRevokeProviderToken(t *testing.T) {
	ctx := context.Background()
	repo := setupTestRepo(t)
	seedSession(t, repo, "t1", "s1", "step1")
	svc := createTestService(repo, newMockStepGetter(), newMockTaskRepo())
	svc.repo = &staleTerminalReadRepo{Repository: repo}
	revoker := &providerSessionRevokeRecorder{}
	svc.SetProviderAccessSessionRevoker(revoker)

	changed, _, err := svc.transitionTaskSessionState(ctx, "t1", "s1", nil,
		models.TaskSessionStateCancelled, "", nil)
	require.NoError(t, err)
	require.False(t, changed)
	require.Empty(t, revoker.calls)
	current, err := repo.GetTaskSession(ctx, "s1")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateStarting, current.State)
}

type rotationAttemptRevoker struct {
	repo *sqliterepo.Repository
	err  error
}

func (r *rotationAttemptRevoker) RevokeSession(ctx context.Context, _ string) error {
	running, err := r.repo.GetExecutorRunningBySessionID(ctx, "s1")
	if err != nil {
		return err
	}
	running.AgentExecutionID = "exec-new"
	r.err = r.repo.UpsertExecutorRunning(ctx, running)
	return nil
}

func TestBootstrapTerminalClaimFencesSuccessorUntilCommit(t *testing.T) {
	ctx := context.Background()
	repo := setupTestRepo(t)
	seedSession(t, repo, "t1", "s1", "step1")
	require.NoError(t, repo.UpdateTaskSessionState(ctx, "s1", models.TaskSessionStateStarting, ""))
	seedExecutorRunning(t, repo, "s1", "t1", "exec-old")
	svc := createTestService(repo, newMockStepGetter(), newMockTaskRepo())
	svc.messageCreator = newServiceBackedMessageCreator(repo)
	revoker := &rotationAttemptRevoker{repo: repo}
	svc.SetProviderAccessSessionRevoker(revoker)

	changed, _, err := svc.transitionBootstrapFailure(ctx, "t1", "s1", "exec-old",
		models.TaskSessionStateStarting, "", "", models.LastAgentError{
			Message: "start failed", OccurredAt: time.Now().UTC(), AgentExecutionID: "exec-old",
			ExecutionID: "exec-old", StampValue: "failure",
		})
	require.NoError(t, err)
	require.True(t, changed)
	require.ErrorIs(t, revoker.err, sqliterepo.ErrTerminalProviderClaimPending)
	running, err := repo.GetExecutorRunningBySessionID(ctx, "s1")
	require.NoError(t, err)
	require.Equal(t, "exec-old", running.AgentExecutionID)
}
