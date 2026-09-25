package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	mcpscope "github.com/kandev/kandev/internal/mcp/scope"
	"github.com/kandev/kandev/internal/orchestrator/messagequeue"
	"github.com/kandev/kandev/internal/task/models"
	ws "github.com/kandev/kandev/pkg/websocket"
	"go.uber.org/zap"
)

type queueRecoveryRequest struct {
	TaskID       string `json:"task_id"`
	SessionID    string `json:"session_id"`
	Cursor       string `json:"cursor"`
	MaxBodyBytes int    `json:"max_body_bytes"`
}

// authorizeChildQueueRead checks the live task relationship on every call.
// The server-derived principal is the only source of caller identity.
func (h *Handlers) authorizeChildQueueRead(ctx context.Context, msg *ws.Message, childID string) (*messagequeue.Service, *ws.Message) {
	caller := h.authorizedQueueCaller(ctx)
	if caller == nil || childID == "" || h.sessionLauncher == nil {
		response, _ := queueRecoveryForbidden(msg)
		return nil, response
	}
	child, err := h.taskSvc.GetTask(ctx, childID)
	if err != nil || child == nil || !canDirectParentAccess(caller, child) || child.ArchivedAt != nil {
		response, _ := queueRecoveryForbidden(msg)
		return nil, response
	}
	queue := h.sessionLauncher.GetMessageQueue()
	if queue == nil {
		response, _ := ws.NewError(msg.ID, msg.Action, ws.ErrorCodeInternalError, "message queue recovery is unavailable", nil)
		return nil, response
	}
	return queue, nil
}

func (h *Handlers) authorizedQueueCaller(ctx context.Context) *models.Task {
	principal, ok := mcpscope.PrincipalFromContext(ctx)
	if !ok || principal.IsAutomation() || principal.WorkspaceID == "" || h.taskSvc == nil || h.sessionRepo == nil {
		return nil
	}
	callerSession, err := h.sessionRepo.GetTaskSession(ctx, principal.CallerSessionID)
	if err != nil || callerSession == nil || callerSession.TaskID != principal.CallerTaskID {
		return nil
	}
	caller, err := h.taskSvc.GetTask(ctx, principal.CallerTaskID)
	if err != nil || caller == nil || caller.WorkspaceID != principal.WorkspaceID || caller.ArchivedAt != nil {
		return nil
	}
	return caller
}

func (h *Handlers) handleGetChildQueueInventory(ctx context.Context, msg *ws.Message) (*ws.Message, error) {
	var req queueRecoveryRequest
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeBadRequest, "invalid payload", nil)
	}
	req.TaskID = strings.TrimSpace(req.TaskID)
	queue, response := h.authorizeChildQueueRead(ctx, msg, req.TaskID)
	if response != nil {
		return response, nil
	}
	sessions, err := h.taskSvc.ListTaskSessions(ctx, req.TaskID)
	if err != nil {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeInternalError, "failed to list child sessions", nil)
	}
	// Reject an unexpectedly large inventory rather than returning a partial
	// result that could be mistaken for a complete recovery receipt.
	if len(sessions) > 256 {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeConflict, "child has more than 256 sessions; inventory is unavailable", nil)
	}
	type sessionInventory struct {
		SessionID string                              `json:"session_id"`
		State     interface{}                         `json:"state"`
		Manifest  *messagequeue.QueueRecoveryManifest `json:"manifest"`
	}
	result := make([]sessionInventory, 0, len(sessions))
	for _, session := range sessions {
		if session == nil || session.TaskID != req.TaskID {
			return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeConflict, "child session ownership changed", nil)
		}
		manifest, err := queue.RecoverySnapshotManifest(ctx, session.ID)
		if err != nil {
			h.logger.Error("child queue inventory failed", zap.String("task_id", req.TaskID), zap.String("session_id", session.ID), zap.Error(err))
			return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeInternalError, "failed to read child queue inventory", nil)
		}
		result = append(result, sessionInventory{SessionID: session.ID, State: session.State, Manifest: manifest})
	}
	return ws.NewResponse(msg.ID, msg.Action, map[string]interface{}{"task_id": req.TaskID, "sessions": result, "total_sessions": len(result)})
}

func (h *Handlers) handleGetChildSessionQueue(ctx context.Context, msg *ws.Message) (*ws.Message, error) {
	var req queueRecoveryRequest
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeBadRequest, "invalid payload", nil)
	}
	req.TaskID = strings.TrimSpace(req.TaskID)
	req.SessionID = strings.TrimSpace(req.SessionID)
	queue, response := h.authorizeChildQueueRead(ctx, msg, req.TaskID)
	if response != nil {
		return response, nil
	}
	if req.SessionID == "" {
		return queueRecoveryForbidden(msg)
	}
	session, err := h.sessionRepo.GetTaskSession(ctx, req.SessionID)
	if err != nil || session == nil || session.TaskID != req.TaskID {
		return queueRecoveryForbidden(msg)
	}
	page, err := queue.RecoverySnapshotPage(ctx, req.SessionID, req.Cursor, req.MaxBodyBytes)
	if err != nil {
		switch {
		case errors.Is(err, messagequeue.ErrInvalidQueueRecoveryPage):
			return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeValidation, err.Error(), nil)
		case errors.Is(err, messagequeue.ErrQueueRecoveryChanged):
			return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeConflict, "queue changed; restart from first page", nil)
		default:
			h.logger.Error("child queue recovery failed", zap.String("task_id", req.TaskID), zap.String("session_id", req.SessionID), zap.Error(err))
			return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeInternalError, "failed to read child queue", nil)
		}
	}
	h.logger.Info("read child queue page", zap.String("task_id", req.TaskID), zap.String("session_id", req.SessionID),
		zap.String("snapshot_sha256", page.SnapshotSHA256), zap.Int("total_entries", page.TotalEntries),
		zap.Int("page_body_bytes", page.PageBodyBytes))
	return ws.NewResponse(msg.ID, msg.Action, map[string]interface{}{"task_id": req.TaskID, "session_id": req.SessionID, "session_state": session.State, "page": page})
}

//nolint:cyclop // Preserve the existing terminal-session scope and error gates.
func (h *Handlers) handleGetTerminalSessionQueue(ctx context.Context, msg *ws.Message) (*ws.Message, error) {
	var req queueRecoveryRequest
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeBadRequest, "invalid payload", nil)
	}
	req.TaskID = strings.TrimSpace(req.TaskID)
	req.SessionID = strings.TrimSpace(req.SessionID)
	principal, ok := mcpscope.PrincipalFromContext(ctx)
	if !ok || principal.WorkspaceID == "" || principal.CallerTaskID != req.TaskID ||
		req.SessionID == "" || principal.CallerSessionID == req.SessionID || h.sessionRepo == nil || h.sessionLauncher == nil {
		return queueRecoveryForbidden(msg)
	}
	target, err := h.sessionRepo.GetTaskSession(ctx, req.SessionID)
	if err != nil || target == nil || target.TaskID != req.TaskID {
		return queueRecoveryForbidden(msg)
	}
	if !isTerminalMessageTargetState(target.State) {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeConflict, "target session is not terminal", nil)
	}
	queue := h.sessionLauncher.GetMessageQueue()
	if queue == nil {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeInternalError, "message queue recovery is unavailable", nil)
	}
	page, err := queue.RecoverySnapshotPage(ctx, req.SessionID, req.Cursor, req.MaxBodyBytes)
	if err != nil {
		switch {
		case errors.Is(err, messagequeue.ErrInvalidQueueRecoveryPage):
			return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeValidation, err.Error(), nil)
		case errors.Is(err, messagequeue.ErrQueueRecoveryChanged):
			return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeConflict, "queue changed; restart from first page", nil)
		default:
			h.logger.Error("terminal queue recovery failed", zap.String("task_id", req.TaskID),
				zap.String("session_id", req.SessionID), zap.Error(err))
			return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeInternalError, "failed to read terminal queue", nil)
		}
	}
	h.logger.Info("read terminal queue page", zap.String("task_id", req.TaskID), zap.String("session_id", req.SessionID),
		zap.String("snapshot_sha256", page.SnapshotSHA256), zap.Int("total_entries", page.TotalEntries),
		zap.Int("page_body_bytes", page.PageBodyBytes), zap.Bool("has_next_page", page.NextCursor != ""))
	return ws.NewResponse(msg.ID, msg.Action, map[string]interface{}{"task_id": req.TaskID, "session_id": req.SessionID, "page": page})
}

func (h *Handlers) handleGetMessageQueueCensus(ctx context.Context, msg *ws.Message) (*ws.Message, error) {
	var req queueRecoveryRequest
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeBadRequest, "invalid payload", nil)
	}
	principal, ok := mcpscope.PrincipalFromContext(ctx)
	if !ok || principal.WorkspaceID == "" || principal.CallerTaskID != req.TaskID || principal.CallerSessionID != req.SessionID ||
		h.sessionLauncher == nil {
		return queueRecoveryForbidden(msg)
	}
	queue := h.sessionLauncher.GetMessageQueue()
	if queue == nil {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeInternalError, "message queue census is unavailable", nil)
	}
	census, err := queue.Census(ctx, req.SessionID)
	if err != nil {
		h.logger.Error("message queue census failed", zap.String("session_id", req.SessionID), zap.Error(err))
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeInternalError, "failed to read message queue census", nil)
	}
	return ws.NewResponse(msg.ID, msg.Action, map[string]interface{}{
		"task_id": req.TaskID, "session_id": req.SessionID, "entries": census.Entries,
		"before_count": census.BeforeCount, "max": census.Max, "auto_run": census.AutoRun,
	})
}

func queueRecoveryForbidden(msg *ws.Message) (*ws.Message, error) {
	return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeForbidden, "queue recovery is limited to the calling task and session", nil)
}
