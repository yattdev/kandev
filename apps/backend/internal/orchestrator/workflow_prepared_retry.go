package orchestrator

import (
	"context"
	"fmt"

	"github.com/kandev/kandev/internal/orchestrator/executor"
	"github.com/kandev/kandev/internal/task/models"
	wfmodels "github.com/kandev/kandev/internal/workflow/models"
	v1 "github.com/kandev/kandev/pkg/api/v1"
)

type workflowRetryTransitionReader interface {
	ListTaskStepTransitions(context.Context, string, int64, int) ([]models.StepTransition, error)
}

// retryPreparedWorkflowSession completes only the exact route whose workspace
// attach failed after its destination session was recorded. It cannot select
// another session or create a worktree. The source remains primary until the
// destination has passed the ordinary workspace inventory and attach checks.
func (s *Service) retryPreparedWorkflowSession(ctx context.Context, req *LaunchSessionRequest) (*LaunchSessionResponse, error) {
	task, err := s.repo.GetTask(ctx, req.TaskID)
	if err != nil || task == nil {
		return nil, fmt.Errorf("load workflow retry task: %w", err)
	}
	route, ok := models.LoadWorkflowSessionRoute(task.Metadata)
	if !ok || route.Phase != workflowSessionRoutePrepared || route.DestinationID == "" ||
		route.DestinationID != req.SessionID || route.SourceSessionID == "" ||
		route.DestinationStepID != task.WorkflowStepID || route.EntryIdentity != s.workflowEntryIdentity(ctx, req.TaskID) ||
		task.State != v1.TaskStateScheduling {
		return nil, fmt.Errorf("prepared workflow route is not current for requested session")
	}
	reader, ok := s.repo.(workflowRetryTransitionReader)
	if !ok {
		return nil, fmt.Errorf("workflow transition reader is unavailable")
	}
	transitions, err := reader.ListTaskStepTransitions(ctx, req.TaskID, 0, 1)
	if err != nil || len(transitions) != 1 || route.EntryIdentity != fmt.Sprintf("entry:%020d", transitions[0].ID) ||
		transitions[0].ToWorkflowStepID == nil || *transitions[0].ToWorkflowStepID != route.DestinationStepID ||
		transitions[0].FromWorkflowStepID == nil {
		return nil, fmt.Errorf("prepared workflow transition is unavailable or stale: %w", err)
	}
	step, err := s.workflowStepGetter.GetStep(ctx, route.DestinationStepID)
	if err != nil || step == nil {
		return nil, fmt.Errorf("load workflow retry step: %w", err)
	}
	// Retry only the simple automatic prompt path. Steps with other entry
	// actions need their full event dispatcher and must not be replayed here.
	if step.SessionTarget != nil || len(step.Events.OnEnter) != 1 || step.Events.OnEnter[0].Type != wfmodels.OnEnterAutoStartAgent {
		return nil, fmt.Errorf("prepared workflow step has non-retryable entry actions")
	}
	sourceStep, err := s.workflowStepGetter.GetStep(ctx, *transitions[0].FromWorkflowStepID)
	if err != nil || sourceStep == nil {
		return nil, fmt.Errorf("load workflow retry source step: %w", err)
	}
	source, err := s.repo.GetTaskSession(ctx, route.SourceSessionID)
	if err != nil || source == nil {
		return nil, fmt.Errorf("load workflow retry source session: %w", err)
	}
	destination, err := s.repo.GetTaskSession(ctx, req.SessionID)
	if err != nil || destination == nil {
		return nil, fmt.Errorf("load workflow retry destination session: %w", err)
	}
	if source.TaskID != req.TaskID || destination.TaskID != req.TaskID ||
		!source.IsPrimary || source.State != models.TaskSessionStateWaitingForInput ||
		destination.IsPrimary || destination.State != models.TaskSessionStateCreated ||
		destination.IsPassthrough ||
		destination.AgentProfileID != route.AgentProfileID ||
		source.TaskEnvironmentID == "" || source.TaskEnvironmentID != destination.TaskEnvironmentID {
		return nil, fmt.Errorf("prepared workflow session ownership changed")
	}
	launchTask, err := s.scheduler.GetTask(ctx, req.TaskID)
	if err != nil || launchTask == nil {
		return nil, fmt.Errorf("load workflow retry launch task: %w", err)
	}
	if _, err := s.executor.LaunchPreparedSession(ctx, launchTask, destination.ID, executor.LaunchOptions{
		AgentProfileID: destination.AgentProfileID, ExecutorID: destination.ExecutorID,
		WorkflowStepID: task.WorkflowStepID, StartAgent: false,
	}); err != nil {
		return nil, fmt.Errorf("attach prepared workflow session: %w", err)
	}
	destination, err = s.repo.GetTaskSession(ctx, destination.ID)
	if err != nil || destination == nil || destination.State != models.TaskSessionStateCreated {
		return nil, fmt.Errorf("prepared workflow destination changed after attach: %w", err)
	}
	if _, err := s.reuseSessionForStepWithEndPolicy(ctx, req.TaskID, source, destination,
		s.resolveStepProfileSessionEndPolicy(sourceStep), &route); err != nil {
		return nil, fmt.Errorf("commit prepared workflow route: %w", err)
	}
	prompt, referenceContext, err := s.buildWorkflowEntryPrompt(ctx, task.Description, step, req.TaskID, destination.ID, false)
	if err != nil {
		return nil, fmt.Errorf("compose prepared workflow prompt: %w", err)
	}
	if err := s.autoStartStepPromptWithPromptContext(ctx, req.TaskID, destination, step, prompt, false, true,
		newStepHandoffOnce(), referenceContext); err != nil {
		return nil, fmt.Errorf("start prepared workflow prompt: %w", err)
	}
	return &LaunchSessionResponse{
		Success: true, TaskID: req.TaskID, SessionID: destination.ID,
		AgentProfileID: destination.AgentProfileID, State: string(models.TaskSessionStateStarting),
	}, nil
}
