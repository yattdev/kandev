package orchestrator

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/kandev/kandev/internal/orchestrator/watcher"
	"github.com/kandev/kandev/internal/task/models"
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
