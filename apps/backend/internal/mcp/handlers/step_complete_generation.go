package handlers

import (
	"context"
	"errors"
	"fmt"
)

func (h *Handlers) validateCompletionTurn(ctx context.Context, sessionID, stepID, turnID string) error {
	required := false
	if h.workflowCtrl != nil {
		resp, err := h.workflowCtrl.GetStep(ctx, stepID)
		if err == nil && resp != nil && resp.Step != nil {
			required = resp.Step.CompletionGatePolicy.RequiresVerdict()
		}
	}
	if turnID == "" {
		if required {
			return errors.New("turn_id from the current prompt is required for gate completion")
		}
		return nil
	}
	reader, ok := h.sessionRepo.(stepCompletionTurnReader)
	if !ok {
		return errors.New("cannot verify calling turn")
	}
	turns, err := reader.ListTurnsBySession(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("cannot verify calling turn: %w", err)
	}
	if len(turns) == 0 || turns[len(turns)-1] == nil || turns[len(turns)-1].ID != turnID {
		return errors.New("calling turn is stale. No signal was recorded. End this turn and resume the session before completing the current step")
	}
	return nil
}
