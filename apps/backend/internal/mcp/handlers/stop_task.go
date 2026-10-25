package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	mcpscope "github.com/kandev/kandev/internal/mcp/scope"
	"github.com/kandev/kandev/internal/orchestrator"
	"github.com/kandev/kandev/internal/task/models"
	taskrepo "github.com/kandev/kandev/internal/task/repository"
	ws "github.com/kandev/kandev/pkg/websocket"
)

const stopTaskStatusKey = "status"

type stopTaskRequest struct {
	TaskID       string `json:"task_id"`
	SenderTaskID string `json:"sender_task_id"`
	OperationID  string `json:"operation_id"`
}

type stopReceiptRequest struct {
	TaskID       string `json:"task_id"`
	SenderTaskID string `json:"sender_task_id"`
	OperationID  string `json:"operation_id"`
}

//nolint:cyclop // Trusted principal attribution and direct-parent validation share one authorization boundary.
func (h *Handlers) handleGetStopReceipt(ctx context.Context, msg *ws.Message) (*ws.Message, error) {
	var req stopReceiptRequest
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeBadRequest, "Invalid payload", nil)
	}
	req.TaskID, req.SenderTaskID, req.OperationID = strings.TrimSpace(req.TaskID), strings.TrimSpace(req.SenderTaskID), strings.TrimSpace(req.OperationID)
	if req.TaskID == "" || req.SenderTaskID == "" || req.OperationID == "" {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeValidation, "task_id, sender_task_id, and operation_id are required", nil)
	}
	principal, hasPrincipal := mcpscope.PrincipalFromContext(ctx)
	senderID := req.SenderTaskID
	trustedCaller := hasPrincipal
	if trustedCaller {
		senderID = principal.CallerTaskID
		if senderID == "" {
			return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeForbidden, "only a task's direct parent in the same workspace can read its stop receipt", nil)
		}
	}
	sender, failure := h.lookupStopTask(ctx, msg, senderID, "sender")
	if failure != nil {
		return failure.response, failure.err
	}
	if trustedCaller && sender.WorkspaceID != principal.WorkspaceID {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeForbidden, "only a task's direct parent in the same workspace can read its stop receipt", nil)
	}
	target, failure := h.lookupStopTask(ctx, msg, req.TaskID, "target")
	if failure != nil {
		return failure.response, failure.err
	}
	if !canStopTask(sender, target) || (trustedCaller && target.WorkspaceID != principal.WorkspaceID) {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeForbidden, "only a task's direct parent in the same workspace can read its stop receipt", nil)
	}
	if h.taskStopper == nil {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeInternalError, "task stop is not configured", nil)
	}
	result, err := h.taskStopper.GetCoordinatorStopReceipt(ctx, target.ID, sender.ID, req.OperationID)
	if err != nil {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeNotFound, "stop receipt not found", nil)
	}
	return ws.NewResponse(msg.ID, msg.Action, map[string]interface{}{
		keyTaskID: target.ID, stopTaskStatusKey: result.Status,
		"receipts": result.Receipts, "session_fences": result.SessionFences,
	})
}

type stopTaskFailure struct {
	response *ws.Message
	err      error
}

//nolint:nestif // Caller authorization is intentionally evaluated before target inventory.
func (h *Handlers) handleStopTask(ctx context.Context, msg *ws.Message) (*ws.Message, error) {
	req, validationError := parseStopTaskRequest(msg)
	if validationError != nil {
		return validationError.response, validationError.err
	}
	if h.stopTaskGetter == nil {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeInternalError, "task lookup is not configured", nil)
	}

	principal, hasPrincipal := mcpscope.PrincipalFromContext(ctx)
	automationCaller := hasPrincipal && principal.IsAutomation()
	var sender *models.Task
	if automationCaller {
		if principal.CallerTaskID == "" {
			return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeForbidden,
				"only a task's direct parent in the same workspace can stop it", nil)
		}
		var lookupError *stopTaskFailure
		sender, lookupError = h.lookupStopTask(ctx, msg, principal.CallerTaskID, "caller")
		if lookupError != nil {
			return lookupError.response, lookupError.err
		}
		if sender.WorkspaceID != principal.WorkspaceID {
			return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeForbidden,
				"only a task's direct parent in the same workspace can stop it", nil)
		}
	} else {
		var lookupError *stopTaskFailure
		sender, lookupError = h.lookupStopTask(ctx, msg, req.SenderTaskID, "sender")
		if lookupError != nil {
			return lookupError.response, lookupError.err
		}
	}

	target, lookupError := h.lookupStopTask(ctx, msg, req.TaskID, "target")
	if lookupError != nil {
		return lookupError.response, lookupError.err
	}

	if !canStopTask(sender, target) || (automationCaller && target.WorkspaceID != principal.WorkspaceID) {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeForbidden,
			"only a task's direct parent in the same workspace can stop it", nil)
	}
	if h.taskStopper == nil {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeInternalError, "task stop is not configured", nil)
	}

	result, err := h.taskStopper.StopTaskForCoordinatorOperation(ctx, target.ID, sender.ID, req.OperationID)
	if err != nil {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeInternalError, "failed to stop target task", nil)
	}
	switch result.Status {
	case orchestrator.CoordinatorTaskStopStatusStopped, orchestrator.CoordinatorTaskStopStatusIncomplete, orchestrator.CoordinatorTaskStopStatusNotRunning:
	default:
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeInternalError, "task stop returned an invalid status", nil)
	}
	return ws.NewResponse(msg.ID, msg.Action, map[string]interface{}{
		keyTaskID:         target.ID,
		stopTaskStatusKey: result.Status,
		"receipts":        result.Receipts,
		"session_fences":  result.SessionFences,
	})
}

func parseStopTaskRequest(msg *ws.Message) (stopTaskRequest, *stopTaskFailure) {
	var req stopTaskRequest
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		return req, newStopTaskFailure(msg, ws.ErrorCodeBadRequest, "Invalid payload: "+err.Error())
	}
	req.TaskID = strings.TrimSpace(req.TaskID)
	req.SenderTaskID = strings.TrimSpace(req.SenderTaskID)
	req.OperationID = strings.TrimSpace(req.OperationID)
	if req.TaskID == "" {
		return req, newStopTaskFailure(msg, ws.ErrorCodeValidation, "task_id is required")
	}
	if req.SenderTaskID == "" {
		return req, newStopTaskFailure(msg, ws.ErrorCodeValidation,
			"sender_task_id is required (the calling agent's MCP server must supply this)")
	}
	if req.OperationID == "" {
		return req, newStopTaskFailure(msg, ws.ErrorCodeValidation, "operation_id is required")
	}
	return req, nil
}

func (h *Handlers) lookupStopTask(
	ctx context.Context,
	msg *ws.Message,
	taskID string,
	role string,
) (*models.Task, *stopTaskFailure) {
	task, err := h.stopTaskGetter(ctx, taskID)
	if err != nil {
		if errors.Is(err, taskrepo.ErrTaskNotFound) {
			return nil, newStopTaskFailure(msg, ws.ErrorCodeNotFound, role+" task not found")
		}
		return nil, newStopTaskFailure(msg, ws.ErrorCodeInternalError, "failed to look up "+role+" task")
	}
	if task == nil {
		return nil, newStopTaskFailure(msg, ws.ErrorCodeNotFound, role+" task not found")
	}
	return task, nil
}

func newStopTaskFailure(msg *ws.Message, code, message string) *stopTaskFailure {
	response, err := ws.NewError(msg.ID, msg.Action, code, message, nil)
	return &stopTaskFailure{response: response, err: err}
}

func canStopTask(sender, target *models.Task) bool {
	return canDirectParentAccess(sender, target)
}
