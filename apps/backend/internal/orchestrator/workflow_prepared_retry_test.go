package orchestrator

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/agent/runtime/lifecycle"
	"github.com/kandev/kandev/internal/orchestrator/executor"
	"github.com/kandev/kandev/internal/task/models"
	taskservice "github.com/kandev/kandev/internal/task/service"
	wfmodels "github.com/kandev/kandev/internal/workflow/models"
	v1 "github.com/kandev/kandev/pkg/api/v1"
	"github.com/stretchr/testify/require"
)

func preparedWorkflowFixture(t *testing.T, failed bool) (*profileSwitchFixture, *wfmodels.WorkflowStep, *wfmodels.WorkflowStep, models.WorkflowSessionRoute) {
	t.Helper()
	ctx := context.Background()
	f := newProfileSwitchFixture(t, models.WorkflowProfileSessionStartPolicyNew, models.WorkflowProfileSessionEndPolicyPark)
	f.svc.SetTurnService(taskservice.NewService(taskservice.Repos{Tasks: f.repo, Sessions: f.repo, Turns: f.repo, Messages: f.repo}, nil, f.svc.logger, taskservice.RepositoryDiscoveryConfig{}))
	sourceStep := &wfmodels.WorkflowStep{ID: "step-a", WorkflowID: "wf1", AgentProfileID: "profile-a",
		ProfileSessionEndPolicy: models.WorkflowProfileSessionEndPolicyPark}
	step := &wfmodels.WorkflowStep{ID: "step-b", WorkflowID: "wf1", AgentProfileID: "profile-b", Prompt: "Run checks",
		ProfileSessionStartPolicy: models.WorkflowProfileSessionStartPolicyNew,
		Events:                    wfmodels.StepEvents{OnEnter: []wfmodels.OnEnterAction{{Type: wfmodels.OnEnterAutoStartAgent}}}}
	f.stepGetter.steps[sourceStep.ID] = sourceStep
	f.stepGetter.steps[step.ID] = step
	task, err := f.repo.GetTask(ctx, "t1")
	require.NoError(t, err)
	task.WorkflowStepID = step.ID
	task.State = v1.TaskStateScheduling
	require.NoError(t, f.repo.UpdateTask(ctx, task))
	f.current.State = models.TaskSessionStateWaitingForInput
	require.NoError(t, f.repo.UpdateTaskSession(ctx, f.current))
	state, lastError := models.TaskSessionStateCreated, ""
	if failed {
		state, lastError = models.TaskSessionStateFailed, "environment preparation failed: base branch does not exist"
	}
	destination := &models.TaskSession{ID: "prepared-destination", TaskID: task.ID, AgentProfileID: "profile-b",
		ExecutorID: f.current.ExecutorID, ExecutorProfileID: f.current.ExecutorProfileID,
		TaskEnvironmentID: f.current.TaskEnvironmentID, State: state, ErrorMessage: lastError, StartedAt: time.Now().UTC()}
	require.NoError(t, f.repo.CreateTaskSession(ctx, destination))
	route := workflowProfileSessionRoute(task.ID, f.current, step, "profile-b",
		f.svc.workflowEntryIdentity(ctx, task.ID), models.WorkflowProfileSessionStartPolicyNew)
	route.DestinationID = destination.ID
	require.NoError(t, f.repo.SetTaskMetadataKey(ctx, task.ID, models.MetaKeyWorkflowSessionRoute, route))
	return f, sourceStep, step, *route
}

func TestPreparedWorkflowReplayUsesExactFailedDestination(t *testing.T) {
	f, sourceStep, step, route := preparedWorkflowFixture(t, true)
	got, _, err := f.svc.prepareWorkflowStepSession(context.Background(), "t1", f.current, step, sourceStep)
	require.NoError(t, err)
	require.Equal(t, route.DestinationID, got.ID)
	sessions, err := f.repo.ListTaskSessions(context.Background(), "t1")
	require.NoError(t, err)
	require.Len(t, sessions, 2, "retry must not allocate a successor session")
}

func TestPreparedWorkflowStartupStartsExactDestinationOnce(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(fmt.Sprintf("failed=%t", failed), func(t *testing.T) {
			f, _, _, route := preparedWorkflowFixture(t, failed)
			ctx := context.Background()
			f.svc.reconcilePreparedWorkflowSessionsOnStartup(ctx)
			task, err := f.repo.GetTask(ctx, "t1")
			require.NoError(t, err)
			recorded, ok := models.LoadWorkflowSessionRoute(task.Metadata)
			require.True(t, ok)
			require.Equal(t, "committed", recorded.Phase)
			require.Equal(t, route.DestinationID, recorded.DestinationID)
			turns, err := f.repo.ListTurnsBySession(ctx, route.DestinationID)
			require.NoError(t, err)
			require.Len(t, turns, 1)
			f.svc.reconcilePreparedWorkflowSessionsOnStartup(ctx)
			repeated, err := f.repo.ListTurnsBySession(ctx, route.DestinationID)
			require.NoError(t, err)
			require.Len(t, repeated, 1)
			require.Equal(t, turns[0].ID, repeated[0].ID)
			sessions, err := f.repo.ListTaskSessions(ctx, "t1")
			require.NoError(t, err)
			require.Len(t, sessions, 2)
		})
	}
}

func TestPreparedWorkflowRetryRefusesFirstTurn(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(fmt.Sprintf("failed=%t", failed), func(t *testing.T) {
			f, sourceStep, step, route := preparedWorkflowFixture(t, failed)
			ctx := context.Background()
			completed := time.Now().UTC()
			require.NoError(t, f.repo.CreateTurn(ctx, &models.Turn{ID: "first-turn", TaskID: "t1",
				TaskSessionID: route.DestinationID, CompletedAt: &completed}))
			got, _, err := f.svc.prepareWorkflowStepSession(ctx, "t1", f.current, step, sourceStep)
			require.Error(t, err)
			require.Nil(t, got)
			f.svc.reconcilePreparedWorkflowSessionsOnStartup(ctx)
			sessions, err := f.repo.ListTaskSessions(ctx, "t1")
			require.NoError(t, err)
			require.Len(t, sessions, 2)
			task, err := f.repo.GetTask(ctx, "t1")
			require.NoError(t, err)
			current, _ := models.LoadWorkflowSessionRoute(task.Metadata)
			require.Equal(t, route, current)
		})
	}
}

func TestPreparedWorkflowRetryRejectsChangedOwnership(t *testing.T) {
	for _, scenario := range []string{"runtime", "memory runtime", "unknown failure", "cancelled", "completed", "profile",
		"source active", "source nonprimary", "stale entry", "environment", "passthrough", "extra action", "archived"} {
		t.Run(scenario, func(t *testing.T) {
			f, sourceStep, step, route := preparedWorkflowFixture(t, true)
			ctx := context.Background()
			destination, err := f.repo.GetTaskSession(ctx, route.DestinationID)
			require.NoError(t, err)
			switch scenario {
			case "memory runtime":
				f.agentMgr.listExecutionsForTaskFunc = func(string) []lifecycle.ExecutionReference {
					return []lifecycle.ExecutionReference{{SessionID: destination.ID, ExecutionID: "live"}}
				}
			case "runtime":
				seedExecutorRunning(t, f.repo, destination.ID, "t1", "live-destination")
			case "unknown failure":
				destination.ErrorMessage = "agent failed after dispatch"
			case "cancelled":
				destination.State = models.TaskSessionStateCancelled
			case "completed":
				destination.State = models.TaskSessionStateCompleted
			case "profile":
				destination.AgentProfileID = "other-profile"
			case "source active":
				f.current.State = models.TaskSessionStateRunning
			case "source nonprimary":
				f.current.IsPrimary = false
			case "stale entry":
				route.EntryIdentity = "entry:00000000000000000000"
				require.NoError(t, f.repo.SetTaskMetadataKey(ctx, "t1", models.MetaKeyWorkflowSessionRoute, route))
			case "environment":
				destination.TaskEnvironmentID = ""
			case "passthrough":
				destination.IsPassthrough = true
			case "extra action":
				step.Events.OnEnter = append(step.Events.OnEnter, wfmodels.OnEnterAction{Type: wfmodels.OnEnterResetAgentContext})
			case "archived":
				require.NoError(t, f.repo.ArchiveTask(ctx, "t1"))
			}
			require.NoError(t, f.repo.UpdateTaskSession(ctx, destination))
			require.NoError(t, f.repo.UpdateTaskSession(ctx, f.current))
			got, _, err := f.svc.prepareWorkflowStepSession(ctx, "t1", f.current, step, sourceStep)
			require.Error(t, err)
			require.Nil(t, got)
			sessions, err := f.repo.ListTaskSessions(ctx, "t1")
			require.NoError(t, err)
			require.Len(t, sessions, 2)
			unchanged, err := f.repo.GetTaskSession(ctx, destination.ID)
			require.NoError(t, err)
			require.Equal(t, destination.State, unchanged.State)
		})
	}
}

func TestPreparedWorkflowConcurrentStartupDoesNotDuplicateFirstTurn(t *testing.T) {
	f, _, _, route := preparedWorkflowFixture(t, true)
	ctx := context.Background()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	f.agentMgr.launchAgentFunc = func(_ context.Context, req *executor.LaunchAgentRequest) (*executor.LaunchAgentResponse, error) {
		if !req.StartAgent {
			once.Do(func() { close(entered) })
			<-release
		}
		return &executor.LaunchAgentResponse{AgentExecutionID: "prepared-execution"}, nil
	}
	done := make(chan struct{})
	go func() { f.svc.reconcilePreparedWorkflowSessionsOnStartup(ctx); close(done) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("startup did not attach the recorded destination")
	}
	second := make(chan error, 1)
	go func() { second <- f.svc.reconcilePreparedWorkflowRoute(ctx, "t1", route) }()
	close(release)
	<-done
	require.NoError(t, <-second)
	turns, err := f.repo.ListTurnsBySession(ctx, route.DestinationID)
	require.NoError(t, err)
	require.Len(t, turns, 1)
	sessions, err := f.repo.ListTaskSessions(ctx, "t1")
	require.NoError(t, err)
	require.Len(t, sessions, 2)
}

func TestPreparedWorkflowStepChangeDuringAttachPreservesSource(t *testing.T) {
	f, sourceStep, step, _ := preparedWorkflowFixture(t, true)
	ctx := context.Background()
	f.agentMgr.launchAgentFunc = func(context.Context, *executor.LaunchAgentRequest) (*executor.LaunchAgentResponse, error) {
		task, err := f.repo.GetTask(ctx, "t1")
		require.NoError(t, err)
		task.WorkflowStepID = "newer-step"
		require.NoError(t, f.repo.UpdateTask(ctx, task))
		return &executor.LaunchAgentResponse{AgentExecutionID: "attached-workspace"}, nil
	}
	got, _, err := f.svc.prepareWorkflowStepSession(ctx, "t1", f.current, step, sourceStep)
	require.Error(t, err)
	require.Nil(t, got)
	source, err := f.repo.GetTaskSession(ctx, f.current.ID)
	require.NoError(t, err)
	require.True(t, source.IsPrimary)
	require.Equal(t, models.TaskSessionStateWaitingForInput, source.State)
}

func TestPreparedWorkflowStartupSweepIsJoinedOnCancellation(t *testing.T) {
	f, _, _, route := preparedWorkflowFixture(t, true)
	entered := make(chan struct{})
	f.agentMgr.launchAgentFunc = func(ctx context.Context, req *executor.LaunchAgentRequest) (*executor.LaunchAgentResponse, error) {
		if !req.StartAgent {
			close(entered)
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return &executor.LaunchAgentResponse{AgentExecutionID: "unexpected"}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.True(t, f.svc.startLifecycleSweepAsync(ctx))
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("joined startup sweep did not reach prepared attach")
	}
	f.svc.stopLifecycleSweepAsync()
	task, err := f.repo.GetTask(context.Background(), "t1")
	require.NoError(t, err)
	current, ok := models.LoadWorkflowSessionRoute(task.Metadata)
	require.True(t, ok)
	require.Equal(t, route, current, "cancelled attach must not commit primary ownership")
	turns, err := f.repo.ListTurnsBySession(context.Background(), route.DestinationID)
	require.NoError(t, err)
	require.Empty(t, turns)
	source, err := f.repo.GetTaskSession(context.Background(), f.current.ID)
	require.NoError(t, err)
	require.True(t, source.IsPrimary)
}

func TestPreparedWorkflowRecoveryLeavesKeptSessionRouteAlone(t *testing.T) {
	f := newProfileSwitchFixture(t, models.WorkflowProfileSessionStartPolicyReuse, models.WorkflowProfileSessionEndPolicyPark)
	step := &wfmodels.WorkflowStep{ID: "step-a", WorkflowID: "wf1", AgentProfileID: "profile-a",
		ProfileSessionStartPolicy: models.WorkflowProfileSessionStartPolicyReuse}
	for range 2 {
		got, switched, err := f.svc.prepareWorkflowStepSession(context.Background(), "t1", f.current, step, nil)
		require.NoError(t, err)
		require.False(t, switched)
		require.Equal(t, f.current.ID, got.ID)
	}
}

func TestPreparedWorkflowRestartAfterPromotionBeforePrompt(t *testing.T) {
	f, sourceStep, step, route := preparedWorkflowFixture(t, true)
	ctx := context.Background()
	_, handled, err := f.svc.retryRecordedPreparedWorkflow(ctx, "t1", &route, step, sourceStep, false)
	require.NoError(t, err)
	require.True(t, handled)
	task, err := f.repo.GetTask(ctx, "t1")
	require.NoError(t, err)
	committed, ok := models.LoadWorkflowSessionRoute(task.Metadata)
	require.True(t, ok)
	require.Equal(t, "committed", committed.Phase)
	before, err := f.repo.ListTurnsBySession(ctx, route.DestinationID)
	require.NoError(t, err)
	require.Empty(t, before)
	f.svc.reconcilePreparedWorkflowSessionsOnStartup(ctx)
	f.svc.reconcileTaskLifecycleTokens(ctx)
	after, err := f.repo.ListTurnsBySession(ctx, route.DestinationID)
	require.NoError(t, err)
	require.Len(t, after, 1, "restart must finish the exact destination's missing first prompt")
}

func TestPreparedWorkflowPendingPromptDoesNotReplayExistingTurn(t *testing.T) {
	f, sourceStep, step, route := preparedWorkflowFixture(t, true)
	ctx := context.Background()
	_, _, err := f.svc.prepareWorkflowStepSession(ctx, "t1", f.current, step, sourceStep)
	require.NoError(t, err)
	require.NoError(t, f.repo.CreateTurn(ctx, &models.Turn{ID: "already-admitted", TaskID: "t1", TaskSessionID: route.DestinationID}))
	f.svc.reconcilePreparedWorkflowSessionsOnStartup(ctx)
	turns, err := f.repo.ListTurnsBySession(ctx, route.DestinationID)
	require.NoError(t, err)
	require.Len(t, turns, 1)
	require.Equal(t, "already-admitted", turns[0].ID)
	sessions, err := f.repo.ListTaskSessions(ctx, "t1")
	require.NoError(t, err)
	require.Len(t, sessions, 2)
}

func TestPreparedWorkflowCommittedRouteWithoutPendingIntentIsNotReplayed(t *testing.T) {
	f, sourceStep, step, route := preparedWorkflowFixture(t, true)
	ctx := context.Background()
	_, _, err := f.svc.prepareWorkflowStepSession(ctx, "t1", f.current, step, sourceStep)
	require.NoError(t, err)
	task, err := f.repo.GetTask(ctx, "t1")
	require.NoError(t, err)
	committed, ok := models.LoadWorkflowSessionRoute(task.Metadata)
	require.True(t, ok)
	committed.FirstPromptPending = false
	require.NoError(t, f.repo.SetTaskMetadataKey(ctx, "t1", models.MetaKeyWorkflowSessionRoute, committed))
	f.svc.reconcilePreparedWorkflowSessionsOnStartup(ctx)
	turns, err := f.repo.ListTurnsBySession(ctx, route.DestinationID)
	require.NoError(t, err)
	require.Empty(t, turns)
}

func TestPreparedWorkflowStartupRefusesChangedStepProfile(t *testing.T) {
	f, _, step, route := preparedWorkflowFixture(t, true)
	ctx := context.Background()
	step.AgentProfileID = "changed-profile"
	f.svc.reconcilePreparedWorkflowSessionsOnStartup(ctx)
	destination, err := f.repo.GetTaskSession(ctx, route.DestinationID)
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateFailed, destination.State)
	require.False(t, destination.IsPrimary)
	turns, err := f.repo.ListTurnsBySession(ctx, route.DestinationID)
	require.NoError(t, err)
	require.Empty(t, turns)
}
