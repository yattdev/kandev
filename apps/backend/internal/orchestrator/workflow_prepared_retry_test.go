package orchestrator

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/orchestrator/executor"
	"github.com/kandev/kandev/internal/task/models"
	wfmodels "github.com/kandev/kandev/internal/workflow/models"
	v1 "github.com/kandev/kandev/pkg/api/v1"
)

func TestRetryPreparedWorkflowSessionUsesRecordedDestination(t *testing.T) {
	ctx := context.Background()
	repo := setupTestRepo(t)
	seedSession(t, repo, "task-retry", "source", "step-source")
	task, err := repo.GetTask(ctx, "task-retry")
	if err != nil {
		t.Fatal(err)
	}
	task.WorkflowStepID = "step-destination"
	task.State = v1.TaskStateScheduling
	if err := repo.UpdateTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	transitionID, err := repo.GetLatestTaskStepTransitionID(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	route := models.WorkflowSessionRoute{
		OperationID: "retry-route", DestinationStepID: "step-destination",
		EntryIdentity: fmt.Sprintf("entry:%020d", transitionID), TargetKind: "profile",
		AgentProfileID: "profile-retry", SourceSessionID: "source",
		DestinationID: "destination", Phase: workflowSessionRoutePrepared,
	}
	if err := repo.SetTaskMetadataKey(ctx, task.ID, models.MetaKeyWorkflowSessionRoute, route); err != nil {
		t.Fatal(err)
	}
	source, err := repo.GetTaskSession(ctx, "source")
	if err != nil {
		t.Fatal(err)
	}
	source.State = models.TaskSessionStateWaitingForInput
	source.IsPrimary = true
	source.AgentProfileID = "profile-retry"
	source.ExecutorID = "exec-local"
	source.TaskEnvironmentID = "environment-retry"
	if err := repo.UpdateTaskSession(ctx, source); err != nil {
		t.Fatal(err)
	}
	seedExecutorRunning(t, repo, source.ID, task.ID, "source-execution")
	if err := repo.CreateTaskEnvironment(ctx, &models.TaskEnvironment{
		ID: "environment-retry", TaskID: task.ID, ExecutorType: string(models.ExecutorTypeLocal),
		Status: models.TaskEnvironmentStatusReady,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateTaskSession(ctx, &models.TaskSession{
		ID: "destination", TaskID: task.ID, State: models.TaskSessionStateCreated,
		AgentProfileID: "profile-retry", ExecutorID: "exec-local",
		TaskEnvironmentID: "environment-retry", StartedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	steps := newMockStepGetter()
	steps.steps["step-source"] = &wfmodels.WorkflowStep{ID: "step-source", WorkflowID: "wf1",
		ProfileSessionEndPolicy: models.WorkflowProfileSessionEndPolicyComplete}
	steps.steps["step-destination"] = &wfmodels.WorkflowStep{
		ID: "step-destination", WorkflowID: "wf1", AgentProfileID: "profile-retry", Prompt: "Run checks",
		Events: wfmodels.StepEvents{OnEnter: []wfmodels.OnEnterAction{{Type: wfmodels.OnEnterAutoStartAgent}}},
	}
	taskRepo := newMockTaskRepo()
	taskRepo.tasks[task.ID] = &v1.Task{ID: task.ID, WorkspaceID: "ws1", WorkflowID: "wf1",
		Title: "Retry task", Description: "task brief", State: v1.TaskStateScheduling}
	agentMgr := &mockAgentManager{repoForExecutionLookup: repo,
		launchAgentFunc: func(context.Context, *executor.LaunchAgentRequest) (*executor.LaunchAgentResponse, error) {
			return &executor.LaunchAgentResponse{AgentExecutionID: "retry-execution"}, nil
		},
	}
	svc := createTestServiceWithScheduler(repo, steps, taskRepo, agentMgr)
	response, err := svc.retryPreparedWorkflowSession(ctx, &LaunchSessionRequest{TaskID: task.ID, SessionID: "destination"})
	if err != nil {
		t.Fatal(err)
	}
	if response.SessionID != "destination" {
		t.Fatalf("session = %q", response.SessionID)
	}
	sessions, err := repo.ListTaskSessions(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("sessions = %d, want 2", len(sessions))
	}
	gotTask, err := repo.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	gotRoute, ok := models.LoadWorkflowSessionRoute(gotTask.Metadata)
	if !ok || gotRoute.Phase != workflowSessionRouteCommitted || gotRoute.DestinationID != "destination" {
		t.Fatalf("route = %+v, found = %t", gotRoute, ok)
	}
	completedSource, err := repo.GetTaskSession(ctx, source.ID)
	if err != nil || completedSource.State != models.TaskSessionStateCompleted {
		t.Fatalf("source state = %v, error = %v", completedSource, err)
	}
	startedDestination, err := repo.GetTaskSession(ctx, "destination")
	if err != nil || startedDestination.State == models.TaskSessionStateCreated {
		t.Fatalf("destination stayed CREATED: %v, error = %v", startedDestination, err)
	}
	if _, err := svc.retryPreparedWorkflowSession(ctx, &LaunchSessionRequest{TaskID: task.ID, SessionID: "destination"}); err == nil {
		t.Fatal("a second retry could start another turn")
	}
}
