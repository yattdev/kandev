package orchestrator

import (
	"context"
	"errors"
	"testing"

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
