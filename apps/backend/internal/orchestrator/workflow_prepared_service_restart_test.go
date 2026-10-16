package orchestrator

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	eventbus "github.com/kandev/kandev/internal/events/bus"
	"github.com/kandev/kandev/internal/orchestrator/executor"
	"github.com/kandev/kandev/internal/task/models"
	taskservice "github.com/kandev/kandev/internal/task/service"
	v1 "github.com/kandev/kandev/pkg/api/v1"
	"github.com/stretchr/testify/require"
)

func preparedRecoveryService(t *testing.T, f *profileSwitchFixture) *Service {
	t.Helper()
	log := testLogger()
	bus := eventbus.NewMemoryEventBus(log)
	t.Cleanup(bus.Close)
	tasks := newMockTaskRepo()
	tasks.tasks["t1"] = &v1.Task{ID: "t1", WorkspaceID: "ws1", WorkflowID: "wf1",
		Title: "Test", Description: "Test", State: v1.TaskStateScheduling}
	svc := NewService(DefaultServiceConfig(), bus, f.agentMgr, tasks, f.repo, nil, nil, nil, log)
	t.Cleanup(func() {
		if svc.GetStatus().Running {
			require.NoError(t, svc.Stop())
		}
	})
	svc.SetWorkflowStepGetter(f.stepGetter)
	turns := taskservice.NewService(taskservice.Repos{Tasks: f.repo, Sessions: f.repo, Turns: f.repo, Messages: f.repo},
		nil, log, taskservice.RepositoryDiscoveryConfig{})
	svc.SetTurnService(turns)
	svc.SetMessageCreator(&serviceBackedWorkflowMessageCreator{serviceBackedMessageCreator: &serviceBackedMessageCreator{svc: turns}})
	svc.messageQueue = newAuthoritativeMemoryQueue(f.repo, log)
	return svc
}

func waitPreparedRecoverySweep(t *testing.T, svc *Service) {
	t.Helper()
	done := make(chan struct{})
	go func() { svc.lifecycleSweepWorkers.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("prepared recovery sweep did not finish")
	}
}

func requirePreparedRecovery(t *testing.T, f *profileSwitchFixture, route models.WorkflowSessionRoute) {
	t.Helper()
	ctx := context.Background()
	task, err := f.repo.GetTask(ctx, "t1")
	require.NoError(t, err)
	current, ok := models.LoadWorkflowSessionRoute(task.Metadata)
	require.True(t, ok)
	require.Equal(t, "committed", current.Phase)
	require.Equal(t, route.DestinationID, current.DestinationID)
	messages, err := f.repo.ListMessages(ctx, route.DestinationID)
	require.NoError(t, err)
	var prompts int
	for _, msg := range messages {
		if msg.AuthorType == models.MessageAuthorUser {
			prompts++
		}
	}
	require.Equal(t, 1, prompts, "a diagnostic status must not count as the workflow prompt")
	turns, err := f.repo.ListTurnsBySession(ctx, route.DestinationID)
	require.NoError(t, err)
	require.Len(t, turns, 1)
	sessions, err := f.repo.ListTaskSessions(ctx, "t1")
	require.NoError(t, err)
	require.Len(t, sessions, 2)
}

func TestPreparedWorkflowServiceAttachFailureThenRestart(t *testing.T) {
	f, _, _, route := preparedWorkflowFixture(t, true)
	var attempts atomic.Int32
	f.agentMgr.launchAgentFunc = func(_ context.Context, req *executor.LaunchAgentRequest) (*executor.LaunchAgentResponse, error) {
		if !req.StartAgent && attempts.Add(1) == 1 {
			return nil, errors.New("environment preparation failed: base branch does not exist")
		}
		return &executor.LaunchAgentResponse{AgentExecutionID: "recovered-execution"}, nil
	}
	ctx := context.Background()
	first := preparedRecoveryService(t, f)
	require.NoError(t, first.Start(ctx))
	waitPreparedRecoverySweep(t, first)
	require.NoError(t, first.Stop())
	turns, err := f.repo.ListTurnsBySession(ctx, route.DestinationID)
	require.NoError(t, err)
	require.Empty(t, turns, "workspace failure must not admit a conversation turn")
	task, err := f.repo.GetTask(ctx, "t1")
	require.NoError(t, err)
	_, found := models.LoadTaskLaunchError(task.Metadata)
	require.True(t, found, "workspace failure diagnostics must remain durable")
	second := preparedRecoveryService(t, f)
	require.NoError(t, second.Start(ctx))
	waitPreparedRecoverySweep(t, second)
	requirePreparedRecovery(t, f, route)
	require.NoError(t, second.Stop())
	third := preparedRecoveryService(t, f)
	require.NoError(t, third.Start(ctx))
	waitPreparedRecoverySweep(t, third)
	requirePreparedRecovery(t, f, route)
	require.NoError(t, third.Stop())
	require.Equal(t, int32(2), attempts.Load())
}

func TestPreparedWorkflowServiceStopDuringAttachThenRestart(t *testing.T) {
	f, _, _, route := preparedWorkflowFixture(t, true)
	entered := make(chan struct{})
	var attempts atomic.Int32
	f.agentMgr.launchAgentFunc = func(ctx context.Context, req *executor.LaunchAgentRequest) (*executor.LaunchAgentResponse, error) {
		if !req.StartAgent && attempts.Add(1) == 1 {
			close(entered)
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return &executor.LaunchAgentResponse{AgentExecutionID: "recovered-execution"}, nil
	}
	ctx := context.Background()
	first := preparedRecoveryService(t, f)
	require.NoError(t, first.Start(ctx))
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("startup did not reach prepared attach")
	}
	require.NoError(t, first.Stop())
	destination, err := f.repo.GetTaskSession(ctx, route.DestinationID)
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateCreated, destination.State)
	require.Empty(t, destination.ErrorMessage)
	turns, err := f.repo.ListTurnsBySession(ctx, route.DestinationID)
	require.NoError(t, err)
	require.Empty(t, turns)
	task, err := f.repo.GetTask(ctx, "t1")
	require.NoError(t, err)
	current, ok := models.LoadWorkflowSessionRoute(task.Metadata)
	require.True(t, ok)
	require.Equal(t, route, current)
	source, err := f.repo.GetTaskSession(ctx, route.SourceSessionID)
	require.NoError(t, err)
	require.True(t, source.IsPrimary)
	second := preparedRecoveryService(t, f)
	require.NoError(t, second.Start(ctx))
	waitPreparedRecoverySweep(t, second)
	requirePreparedRecovery(t, f, route)
	require.NoError(t, second.Stop())
	third := preparedRecoveryService(t, f)
	require.NoError(t, third.Start(ctx))
	waitPreparedRecoverySweep(t, third)
	requirePreparedRecovery(t, f, route)
	require.NoError(t, third.Stop())
}
