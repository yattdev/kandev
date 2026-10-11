package orchestrator

import (
	"context"
	"fmt"

	"github.com/kandev/kandev/internal/orchestrator/executor"
	"github.com/kandev/kandev/internal/task/models"
	wfmodels "github.com/kandev/kandev/internal/workflow/models"
	"go.uber.org/zap"
)

type preparedWorkflowPromotionKey struct{}

type preparedWorkflowRoutePromoter interface {
	PromotePreparedWorkflowSession(context.Context, string, models.WorkflowSessionRoute) (bool, error)
}

type preparedWorkflowRearmStore interface {
	RearmPreparedWorkflowSession(context.Context, string, models.WorkflowSessionRoute) (bool, error)
}

func simplePreparedWorkflowStep(step *wfmodels.WorkflowStep) bool {
	return step != nil && step.SessionTarget == nil && len(step.Events.OnEnter) == 1 &&
		step.Events.OnEnter[0].Type == wfmodels.OnEnterAutoStartAgent
}

// retryRecordedPreparedWorkflow consumes only the exact profile route. Refusal
// is an error, never permission to allocate another destination.
func (s *Service) retryRecordedPreparedWorkflow(ctx context.Context, taskID string, expected *models.WorkflowSessionRoute,
	step, sourceStep *wfmodels.WorkflowStep, startPrompt bool) (*models.TaskSession, bool, error) {
	if expected == nil {
		return nil, false, nil
	}
	release := s.acquireSessionRouteOperationLock("prepared-workflow:" + taskID)
	defer release()
	route, destination, err := s.loadPreparedWorkflowRetryRoute(ctx, taskID, expected, step)
	if err != nil {
		return nil, true, err
	}
	if route == nil {
		return nil, false, nil
	}
	if route.Phase == workflowSessionRouteCommitted {
		if startPrompt {
			err = s.resumePendingWorkflowFirstPrompt(ctx, taskID, *route, destination, step, sourceStep)
		}
		return destination, true, err
	}
	if route.Phase != workflowSessionRoutePrepared || sourceStep == nil {
		return nil, true, fmt.Errorf("prepared workflow route cannot be retried")
	}
	if err := s.rearmPreparedWorkflowRoute(ctx, taskID, *route); err != nil {
		return nil, true, err
	}
	recovered, err := s.attachPreparedWorkflowDestination(ctx, taskID, *route, sourceStep)
	if err == nil && startPrompt {
		err = s.startPreparedWorkflowPrompt(ctx, taskID, recovered, step)
		if err == nil {
			if store, ok := s.repo.(pendingWorkflowFirstPromptStore); ok {
				err = store.CompletePendingWorkflowFirstPrompt(ctx, taskID, *route)
			}
		}
	}
	return recovered, true, err
}

func (s *Service) loadPreparedWorkflowRetryRoute(ctx context.Context, taskID string, expected *models.WorkflowSessionRoute,
	step *wfmodels.WorkflowStep) (*models.WorkflowSessionRoute, *models.TaskSession, error) {
	task, err := s.repo.GetTask(ctx, taskID)
	if err != nil {
		return nil, nil, err
	}
	if task == nil {
		return nil, nil, fmt.Errorf("prepared workflow task is missing")
	}
	route, ok := models.LoadWorkflowSessionRoute(task.Metadata)
	if !ok || !preparedWorkflowReplacementMatches(route, *expected) {
		return nil, nil, nil
	}
	if route.Phase != workflowSessionRouteCommitted && !simplePreparedWorkflowStep(step) {
		return nil, nil, fmt.Errorf("prepared workflow step has non-retryable entry actions")
	}
	if !workflowSessionRouteMatchesStep(&route, step, expected.AgentProfileID) ||
		route.EntryIdentity != s.workflowEntryIdentity(ctx, taskID) || task.WorkflowStepID != step.ID {
		return nil, nil, fmt.Errorf("prepared workflow route is stale")
	}
	destination, err := s.repo.GetTaskSession(ctx, route.DestinationID)
	if err != nil {
		return nil, nil, err
	}
	if route.Phase == workflowSessionRouteCommitted {
		if destination == nil || destination.TaskID != taskID || isTerminalSessionState(destination.State) {
			return nil, nil, fmt.Errorf("recorded workflow destination is terminal")
		}
		return &route, destination, nil
	}
	return &route, destination, nil
}

func preparedWorkflowReplacementMatches(route, expected models.WorkflowSessionRoute) bool {
	return route.OperationID == expected.OperationID && route.DestinationID != "" && route.SourceSessionID != route.DestinationID
}

func (s *Service) rearmPreparedWorkflowRoute(ctx context.Context, taskID string, route models.WorkflowSessionRoute) error {
	if s.agentManager == nil {
		return fmt.Errorf("prepared workflow runtime observer is unavailable")
	}
	for _, runtime := range s.agentManager.ListExecutionsForTask(taskID) {
		if runtime.SessionID == route.DestinationID {
			return fmt.Errorf("prepared workflow destination still owns a runtime")
		}
	}
	rearmer, ok := s.repo.(preparedWorkflowRearmStore)
	if !ok {
		return fmt.Errorf("conditional prepared workflow rearm is unavailable")
	}
	changed, err := rearmer.RearmPreparedWorkflowSession(ctx, taskID, route)
	if err != nil {
		return err
	}
	if !changed {
		return fmt.Errorf("prepared workflow session ownership changed or already has execution evidence")
	}
	return nil
}

func (s *Service) attachPreparedWorkflowDestination(ctx context.Context, taskID string, route models.WorkflowSessionRoute,
	sourceStep *wfmodels.WorkflowStep) (*models.TaskSession, error) {
	source, err := s.repo.GetTaskSession(ctx, route.SourceSessionID)
	if err != nil {
		return nil, err
	}
	destination, err := s.repo.GetTaskSession(ctx, route.DestinationID)
	if err != nil {
		return nil, err
	}
	task, err := s.scheduler.GetTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, fmt.Errorf("prepared workflow launch task is missing")
	}
	if _, err := s.executor.LaunchPreparedSession(ctx, task, destination.ID, executor.LaunchOptions{
		AgentProfileID: destination.AgentProfileID, ExecutorID: destination.ExecutorID,
		WorkflowStepID: route.DestinationStepID, StartAgent: false,
	}); err != nil {
		return nil, fmt.Errorf("attach prepared workflow destination: %w", err)
	}
	destination, err = s.repo.GetTaskSession(ctx, destination.ID)
	if err != nil {
		return nil, err
	}
	if destination == nil || destination.State != models.TaskSessionStateCreated {
		return nil, fmt.Errorf("prepared workflow destination changed during attach")
	}
	promotionCtx := context.WithValue(ctx, preparedWorkflowPromotionKey{}, true)
	recovered, err := s.reuseSessionForStepWithEndPolicy(promotionCtx, taskID, source, destination,
		s.resolveStepProfileSessionEndPolicy(sourceStep), &route)
	if err != nil {
		return nil, err
	}
	step, err := s.workflowStepGetter.GetStep(ctx, route.DestinationStepID)
	if err != nil {
		return nil, err
	}
	if err := s.recordWorkflowSourceBinding(ctx, taskID, step, recovered); err != nil {
		return nil, err
	}
	return recovered, nil
}

// This runs in the joined startup lifecycle sweep, after listeners are ready.
func (s *Service) reconcilePreparedWorkflowSessionsOnStartup(ctx context.Context) {
	lister, ok := s.repo.(lifecycleTaskMetadataLister)
	if !ok || s.workflowStepGetter == nil {
		return
	}
	tasks, err := lister.ListTasksWithMetadataKey(ctx, models.MetaKeyWorkflowSessionRoute)
	if err != nil {
		s.logger.Warn("list prepared workflow routes", zap.Error(err))
		return
	}
	for _, task := range tasks {
		if ctx.Err() != nil {
			return
		}
		route, ok := models.LoadWorkflowSessionRoute(task.Metadata)
		if !ok || !recoverablePreparedWorkflowRoute(route) || route.DestinationID == "" {
			continue
		}
		if err := s.reconcilePreparedWorkflowRoute(ctx, task.ID, route); err != nil {
			s.logger.Warn("reconcile prepared workflow route", zap.String("task_id", task.ID), zap.Error(err))
		}
	}
}

func recoverablePreparedWorkflowRoute(route models.WorkflowSessionRoute) bool {
	return route.Phase == workflowSessionRoutePrepared || (route.Phase == workflowSessionRouteCommitted && route.FirstPromptPending)
}

func (s *Service) reconcilePreparedWorkflowRoute(ctx context.Context, taskID string, route models.WorkflowSessionRoute) error {
	step, err := s.workflowStepGetter.GetStep(ctx, route.DestinationStepID)
	if err != nil {
		return err
	}
	if !simplePreparedWorkflowStep(step) {
		return nil
	}
	profileID, err := s.resolveStepAgentProfileForTaskID(ctx, taskID, step)
	if err != nil {
		return err
	}
	if profileID != route.AgentProfileID {
		return fmt.Errorf("prepared workflow step profile changed")
	}
	reader, ok := s.repo.(interface {
		ListTaskStepTransitions(context.Context, string, int64, int) ([]models.StepTransition, error)
	})
	if !ok {
		return fmt.Errorf("workflow transition reader is unavailable")
	}
	transitions, err := reader.ListTaskStepTransitions(ctx, taskID, 0, 1)
	if err != nil {
		return err
	}
	if len(transitions) != 1 || transitions[0].FromWorkflowStepID == nil {
		return fmt.Errorf("prepared workflow transition is unavailable")
	}
	sourceStep, err := s.workflowStepGetter.GetStep(ctx, *transitions[0].FromWorkflowStepID)
	if err != nil {
		return err
	}
	_, _, err = s.retryRecordedPreparedWorkflow(ctx, taskID, &route, step, sourceStep, true)
	return err
}

// The caller holds prepared route serialization through first-prompt admission.
func (s *Service) startPreparedWorkflowPrompt(ctx context.Context, taskID string, destination *models.TaskSession, step *wfmodels.WorkflowStep) error {
	task, err := s.repo.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	prompt, referenceContext, err := s.buildWorkflowEntryPrompt(ctx, task.Description, step, taskID, destination.ID, destination.QueueIncarnationID, false)
	if err != nil {
		return err
	}
	return s.autoStartStepPromptWithPromptContext(ctx, taskID, destination, step, prompt, false, false,
		newStepHandoffOnce(), referenceContext)
}
