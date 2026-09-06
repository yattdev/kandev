package handlers

import (
	"context"
	"encoding/json"
	"strings"

	mcpscope "github.com/kandev/kandev/internal/mcp/scope"
	"github.com/kandev/kandev/internal/task/models"
	ws "github.com/kandev/kandev/pkg/websocket"
	"go.uber.org/zap"
)

type sessionCleanupRequest struct {
	TaskID          string                            `json:"task_id"`
	CallerSessionID string                            `json:"caller_session_id"`
	TargetSessionID string                            `json:"target_session_id"`
	ExistingReceipt *models.TaskSessionCleanupReceipt `json:"-"`
}

func (h *Handlers) handleRecoverSessionQueue(ctx context.Context, msg *ws.Message) (*ws.Message, error) {
	req, response := h.authorizeSessionCleanup(ctx, msg)
	if response != nil {
		return response, nil
	}
	if h.queueManager == nil {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeInternalError, "message queue recovery is not available", nil)
	}
	entries, err := h.queueManager.RecoverSessionQueue(ctx, req.TargetSessionID, req.CallerSessionID)
	if err != nil {
		h.logger.Error("session queue recovery failed", zap.String("target_session_id", req.TargetSessionID), zap.Error(err))
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeInternalError, "failed to recover session queue", nil)
	}
	return ws.NewResponse(msg.ID, msg.Action, map[string]interface{}{
		"task_id": req.TaskID, "source_session_id": req.TargetSessionID,
		"destination_session_id": req.CallerSessionID,
		"entries":                entries, "readback_count": len(entries),
	})
}

func (h *Handlers) handleCloseTaskSession(ctx context.Context, msg *ws.Message) (*ws.Message, error) {
	req, response := h.authorizeSessionCleanup(ctx, msg)
	if response != nil {
		return response, nil
	}
	reader, ok := h.sessionRepo.(sessionCleanupReceiptReader)
	if !ok || h.sessionCloser == nil {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeInternalError, "session cleanup is not available", nil)
	}
	if req.ExistingReceipt != nil {
		return ws.NewResponse(msg.ID, msg.Action, map[string]interface{}{"receipt": req.ExistingReceipt})
	}
	if err := h.sessionCloser.DeleteSession(ctx, req.TargetSessionID); err != nil {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeValidation, err.Error(), nil)
	}
	receipt, err := reader.GetTaskSessionCleanupReceipt(ctx, req.TaskID, req.TargetSessionID)
	if err != nil {
		h.logger.Error("session cleanup receipt readback failed",
			zap.String("task_id", req.TaskID), zap.String("session_id", req.TargetSessionID), zap.Error(err))
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeInternalError, "session cleanup committed without a readable receipt", nil)
	}
	return ws.NewResponse(msg.ID, msg.Action, map[string]interface{}{"receipt": receipt})
}

func (h *Handlers) authorizeSessionCleanup(
	ctx context.Context,
	msg *ws.Message,
) (sessionCleanupRequest, *ws.Message) {
	req, response := decodeSessionCleanupRequest(msg)
	if response != nil {
		return req, response
	}
	workspaceID, response := h.authorizeSessionCleanupCaller(ctx, msg, req)
	if response != nil {
		return req, response
	}
	return req, h.authorizeSessionCleanupTarget(ctx, msg, &req, workspaceID)
}

func decodeSessionCleanupRequest(msg *ws.Message) (sessionCleanupRequest, *ws.Message) {
	var req sessionCleanupRequest
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		return req, sessionCleanupError(msg, ws.ErrorCodeBadRequest, "Invalid payload: "+err.Error())
	}
	req.TaskID = strings.TrimSpace(req.TaskID)
	req.CallerSessionID = strings.TrimSpace(req.CallerSessionID)
	req.TargetSessionID = strings.TrimSpace(req.TargetSessionID)
	return req, nil
}

func (h *Handlers) authorizeSessionCleanupCaller(
	ctx context.Context,
	msg *ws.Message,
	req sessionCleanupRequest,
) (string, *ws.Message) {
	principal, ok := mcpscope.PrincipalFromContext(ctx)
	if !ok || principal.WorkspaceID == "" || req.TaskID == "" || req.CallerSessionID == "" || req.TargetSessionID == "" ||
		principal.CallerTaskID != req.TaskID || principal.CallerSessionID != req.CallerSessionID {
		return "", sessionCleanupError(msg, ws.ErrorCodeForbidden,
			"session cleanup is limited to the calling task's current primary session")
	}
	caller, err := h.sessionRepo.GetTaskSession(ctx, req.CallerSessionID)
	if err != nil || caller.TaskID != req.TaskID || !caller.IsPrimary {
		return "", sessionCleanupError(msg, ws.ErrorCodeForbidden,
			"session cleanup requires the calling task's current primary session")
	}
	if req.TargetSessionID == req.CallerSessionID {
		return "", sessionCleanupError(msg, ws.ErrorCodeForbidden, "the current primary session cannot be closed or recovered")
	}
	return principal.WorkspaceID, nil
}

func (h *Handlers) authorizeSessionCleanupTarget(
	ctx context.Context,
	msg *ws.Message,
	req *sessionCleanupRequest,
	workspaceID string,
) *ws.Message {
	target, err := h.sessionRepo.GetTaskSession(ctx, req.TargetSessionID)
	if err != nil {
		receipt := h.readExistingCleanupReceipt(ctx, req, workspaceID, msg.Action)
		if receipt != nil {
			req.ExistingReceipt = receipt
			return nil
		}
		return sessionCleanupError(msg, ws.ErrorCodeForbidden, "target session is outside the calling task")
	}
	if target.TaskID != req.TaskID || target.IsPrimary {
		return sessionCleanupError(msg, ws.ErrorCodeForbidden, "target session must be a non-primary session on the calling task")
	}
	if !isTerminalCleanupState(target.State) {
		return sessionCleanupError(msg, ws.ErrorCodeValidation, "target session must be terminal before cleanup")
	}
	return nil
}

func (h *Handlers) readExistingCleanupReceipt(
	ctx context.Context,
	req *sessionCleanupRequest,
	workspaceID, action string,
) *models.TaskSessionCleanupReceipt {
	reader, ok := h.sessionRepo.(sessionCleanupReceiptReader)
	if !ok || action != ws.ActionMCPCloseTaskSession {
		return nil
	}
	receipt, err := reader.GetTaskSessionCleanupReceipt(ctx, req.TaskID, req.TargetSessionID)
	if err != nil || receipt.WorkspaceID != workspaceID {
		return nil
	}
	return receipt
}

func sessionCleanupError(msg *ws.Message, code, message string) *ws.Message {
	response, _ := ws.NewError(msg.ID, msg.Action, code, message, nil)
	return response
}

func isTerminalCleanupState(state models.TaskSessionState) bool {
	return state == models.TaskSessionStateCompleted ||
		state == models.TaskSessionStateFailed || state == models.TaskSessionStateCancelled
}
