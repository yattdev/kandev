package executor

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

func TestPreparedAttachShutdownPreservesOnlyUnstartedReuse(t *testing.T) {
	for _, scenario := range []struct {
		name         string
		cause        error
		launchErr    error
		startAgent   bool
		reuse        bool
		initialState models.TaskSessionState
		wantState    models.TaskSessionState
	}{
		{"owned shutdown", ErrStartupSweepStopped, fmt.Errorf("attach: %w", context.Canceled), false, true,
			models.TaskSessionStateCreated, models.TaskSessionStateCreated},
		{"caller cancellation", context.Canceled, context.Canceled, false, true,
			models.TaskSessionStateCreated, models.TaskSessionStateFailed},
		{"unknown failure during shutdown", ErrStartupSweepStopped, errors.New("provider failed"), false, true,
			models.TaskSessionStateCreated, models.TaskSessionStateFailed},
		{"agent startup", ErrStartupSweepStopped, context.Canceled, true, true,
			models.TaskSessionStateCreated, models.TaskSessionStateFailed},
		{"fresh workspace", ErrStartupSweepStopped, context.Canceled, false, false,
			models.TaskSessionStateCreated, models.TaskSessionStateFailed},
		{"explicitly cancelled session", ErrStartupSweepStopped, context.Canceled, false, true,
			models.TaskSessionStateCancelled, models.TaskSessionStateCancelled},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			repo := newMockRepository()
			repo.sessions["destination"] = &models.TaskSession{ID: "destination", TaskID: "task-1", State: scenario.initialState}
			exec := newTestExecutor(t, &mockAgentManager{}, repo)
			ctx, cancel := context.WithCancelCause(context.Background())
			cancel(scenario.cause)
			err := exec.handlePreparedLaunchFailure(ctx, &LaunchAgentRequest{
				TaskID: "task-1", SessionID: "destination", StartAgent: scenario.startAgent, WorkspaceReuseRequired: scenario.reuse,
			}, "", "", scenario.launchErr)
			require.ErrorIs(t, err, scenario.launchErr)
			session, err := repo.GetTaskSession(context.Background(), "destination")
			require.NoError(t, err)
			require.Equal(t, scenario.wantState, session.State)
		})
	}
}
