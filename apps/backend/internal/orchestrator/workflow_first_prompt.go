package orchestrator

import (
	"context"
	"fmt"

	"github.com/kandev/kandev/internal/task/models"
	wfmodels "github.com/kandev/kandev/internal/workflow/models"
)

type pendingWorkflowFirstPromptStore interface {
	ValidatePendingWorkflowFirstPrompt(context.Context, string, models.WorkflowSessionRoute) (bool, error)
	CompletePendingWorkflowFirstPrompt(context.Context, string, models.WorkflowSessionRoute) error
}

// Called under prepared-route serialization. A committed recovery can finish
// its first prompt, but cannot resurrect FAILED or already-dispatched sessions.
func (s *Service) resumePendingWorkflowFirstPrompt(ctx context.Context, taskID string, route models.WorkflowSessionRoute,
	destination *models.TaskSession, step, sourceStep *wfmodels.WorkflowStep) error {
	if !route.FirstPromptPending || !simplePreparedWorkflowStep(step) || sourceStep == nil {
		return nil
	}
	store, ok := s.repo.(pendingWorkflowFirstPromptStore)
	if !ok {
		return fmt.Errorf("pending workflow prompt store is unavailable")
	}
	valid, err := store.ValidatePendingWorkflowFirstPrompt(ctx, taskID, route)
	if err != nil {
		return err
	}
	if !valid {
		return nil
	}
	if s.agentManager.IsAgentRunningForSession(ctx, destination.ID) {
		return nil
	}
	if err := s.settlePendingWorkflowSource(ctx, taskID, route, step, sourceStep); err != nil {
		return err
	}
	valid, err = store.ValidatePendingWorkflowFirstPrompt(ctx, taskID, route)
	if err != nil || !valid {
		return err
	}
	destination, err = s.repo.GetTaskSession(ctx, destination.ID)
	if err != nil {
		return err
	}
	if err := s.startPreparedWorkflowPrompt(ctx, taskID, destination, step); err != nil {
		return err
	}
	return store.CompletePendingWorkflowFirstPrompt(ctx, taskID, route)
}

func (s *Service) settlePendingWorkflowSource(ctx context.Context, taskID string, route models.WorkflowSessionRoute,
	step, sourceStep *wfmodels.WorkflowStep) error {
	source, err := s.repo.GetTaskSession(ctx, route.SourceSessionID)
	if err != nil {
		return err
	}
	if source == nil || source.IsPrimary {
		return fmt.Errorf("pending workflow source ownership changed")
	}
	parking, parked := models.LoadWorkflowParking(source.Metadata)
	settled := source.State == models.TaskSessionStateCompleted || (parked &&
		parking.RouteOperationID == route.OperationID && parking.EntryIdentity == route.EntryIdentity &&
		parking.DestinationID == route.DestinationID)
	if !settled {
		if _, err := s.finishWorkflowProfileSwitchSource(ctx, taskID, source, s.resolveStepProfileSessionEndPolicy(sourceStep)); err != nil {
			return err
		}
	}
	if err := s.transferWorkflowProfileSwitchQueue(ctx, source.ID, route.DestinationID); err != nil {
		return err
	}
	destination, err := s.repo.GetTaskSession(ctx, route.DestinationID)
	if err != nil {
		return err
	}
	if destination == nil {
		return fmt.Errorf("pending workflow destination is missing")
	}
	return s.recordWorkflowSourceBinding(ctx, taskID, step, destination)
}
