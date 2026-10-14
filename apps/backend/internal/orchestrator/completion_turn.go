package orchestrator

import (
	"context"
	"fmt"

	"github.com/kandev/kandev/internal/sysprompt"
	"github.com/kandev/kandev/internal/task/models"
	wfmodels "github.com/kandev/kandev/internal/workflow/models"
)

type completionTurnReader interface {
	ListTurnsBySession(context.Context, string) ([]*models.Turn, error)
}

func (s *Service) completionSignalMatchesTurn(ctx context.Context, session *models.TaskSession, signal models.PendingStepCompletionSignal) bool {
	if signal.TurnID == "" {
		return false
	}
	reader, ok := s.repo.(completionTurnReader)
	if !ok {
		return false
	}
	turns, err := reader.ListTurnsBySession(ctx, session.ID)
	if err != nil || len(turns) == 0 {
		return false
	}
	checker, ok := s.repo.(interface {
		CompletionTurnCurrent(context.Context, string, string, string, string) (bool, error)
	})
	if !ok {
		return false
	}
	current, err := checker.CompletionTurnCurrent(ctx, session.TaskID, session.ID, signal.StepID, signal.TurnID)
	if err != nil || !current {
		return false
	}
	turn := turns[len(turns)-1]
	return turn != nil && turn.ID == signal.TurnID && turn.TaskID == session.TaskID && models.StringFromAny(turn.Metadata[models.TurnMetaKeyWorkflowStepIDAtStart]) == signal.StepID
}

func (s *Service) completionTurnPrompt(ctx context.Context, taskID, turnID, prompt string) string {
	if turnID == "" || s.workflowStepGetter == nil {
		return prompt
	}
	task, err := s.repo.GetTask(ctx, taskID)
	if err != nil || task == nil {
		return prompt
	}
	step, err := s.workflowStepGetter.GetStep(ctx, task.WorkflowStepID)
	if err != nil || step == nil || !step.CompletionGatePolicy.RequiresVerdict() {
		return prompt
	}
	return sysprompt.Wrap(fmt.Sprintf("Kandev completion turn ID: %s. Pass this exact ID as turn_id to step_complete_kandev. A Review or QA gate requires verdict PASS; absent or failed verdicts do not advance.", turnID)) + "\n\n" + prompt
}

func completionAdmissionContext(ctx context.Context, step *wfmodels.WorkflowStep, session *models.TaskSession) context.Context {
	if step == nil || !step.CompletionGatePolicy.RequiresVerdict() {
		return ctx
	}
	signal, _ := models.LoadPendingStepSignal(session.Metadata)
	return models.WithStepCompletionAdmission(ctx, models.StepCompletionAdmission{TaskID: session.TaskID, SessionID: session.ID, StepID: step.ID, TurnID: signal.TurnID})
}
