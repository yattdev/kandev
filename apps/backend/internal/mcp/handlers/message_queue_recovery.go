package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	mcpscope "github.com/kandev/kandev/internal/mcp/scope"
	"github.com/kandev/kandev/internal/orchestrator/messagequeue"
	ws "github.com/kandev/kandev/pkg/websocket"
	"go.uber.org/zap"
)

type queueRecoveryRequest struct {
	TaskID       string `json:"task_id"`
	SessionID    string `json:"session_id"`
	Cursor       string `json:"cursor"`
	MaxBodyBytes int    `json:"max_body_bytes"`
}

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
