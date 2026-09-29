package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/kandev/kandev/internal/mcp/scope"
	"github.com/kandev/kandev/internal/orchestrator/messagequeue"
	ws "github.com/kandev/kandev/pkg/websocket"
)

type messageQueueScopeRequest struct {
	TaskID    string `json:"task_id"`
	SessionID string `json:"session_id"`
}

type removeMessageQueueEntryRequest struct {
	messageQueueScopeRequest
	EntryID string `json:"entry_id"`
}

type queueSnapshotter interface {
	Snapshot(context.Context, messagequeue.QueueSessionIdentity) (*messagequeue.QueueStatus, error)
}

type queueIdentityResolver interface {
	ResolveSessionIdentity(context.Context, string, string) (messagequeue.QueueSessionIdentity, error)
}

type messageQueueCensusEntry struct {
	ID          string `json:"id"`
	Position    int64  `json:"position"`
	QueuedAt    string `json:"queued_at"`
	QueuedBy    string `json:"queued_by"`
	ContentHash string `json:"content_hash"`
	ContentSize int    `json:"content_size"`
}

func (h *Handlers) handleGetMessageQueueCensus(ctx context.Context, msg *ws.Message) (*ws.Message, error) {
	var req messageQueueScopeRequest
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeBadRequest, "invalid payload", nil)
	}
	identity, response, err := h.authorizeOwnMessageQueue(ctx, msg, req)
	if response != nil {
		return response, err
	}
	snapshotter, ok := h.messageQueue.(queueSnapshotter)
	if !ok {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeInternalError, "message queue census is not available", nil)
	}
	status, err := snapshotter.Snapshot(ctx, identity)
	if err != nil {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeInternalError, "failed to read message queue census", nil)
	}
	entries := make([]messageQueueCensusEntry, 0, len(status.Entries))
	for _, entry := range status.Entries {
		digest := sha256.Sum256([]byte(entry.Content))
		entries = append(entries, messageQueueCensusEntry{
			ID: entry.ID, Position: entry.Position, QueuedAt: entry.QueuedAt.UTC().Format("2006-01-02T15:04:05.999999999Z"),
			QueuedBy: entry.QueuedBy, ContentHash: hex.EncodeToString(digest[:]), ContentSize: len(entry.Content),
		})
	}
	return ws.NewResponse(msg.ID, msg.Action, map[string]any{
		"task_id": identity.TaskID, "session_id": identity.SessionID, "entries": entries,
		"count": status.Count, "max": status.Max,
	})
}

func (h *Handlers) handleRemoveMessageQueueEntry(ctx context.Context, msg *ws.Message) (*ws.Message, error) {
	var req removeMessageQueueEntryRequest
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeBadRequest, "invalid payload", nil)
	}
	if strings.TrimSpace(req.EntryID) == "" {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeValidation, "entry_id is required", nil)
	}
	identity, response, err := h.authorizeOwnMessageQueue(ctx, msg, req.messageQueueScopeRequest)
	if response != nil {
		return response, err
	}
	removed, err := h.messageQueue.RemoveEntryForSession(ctx, identity, req.EntryID)
	if err != nil {
		if err == messagequeue.ErrEntryNotFound {
			return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeNotFound, "queue entry not found", nil)
		}
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeInternalError, "failed to remove message queue entry", nil)
	}
	return ws.NewResponse(msg.ID, msg.Action, map[string]any{"removed": len(removed.Removed) == 1})
}

func (h *Handlers) authorizeOwnMessageQueue(ctx context.Context, msg *ws.Message, req messageQueueScopeRequest) (messagequeue.QueueSessionIdentity, *ws.Message, error) {
	taskID, sessionID := strings.TrimSpace(req.TaskID), strings.TrimSpace(req.SessionID)
	principal, ok := scope.PrincipalFromContext(ctx)
	if !ok || principal.WorkspaceID == "" || taskID == "" || sessionID == "" || principal.CallerTaskID != taskID || principal.CallerSessionID != sessionID {
		return queueAccessForbidden(msg)
	}
	identity, err := h.resolveMessageQueueIdentity(ctx, taskID, sessionID)
	if err != nil {
		return queueAccessForbidden(msg)
	}
	return identity, nil, nil
}

func (h *Handlers) resolveMessageQueueIdentity(ctx context.Context, taskID, sessionID string) (messagequeue.QueueSessionIdentity, error) {
	if h.taskSvc != nil {
		if h.taskSvc.AuthorizeTaskAccess(ctx, taskID) != nil || h.taskSvc.AuthorizeSessionAccess(ctx, sessionID) != nil {
			return messagequeue.QueueSessionIdentity{}, messagequeue.ErrSessionIdentityMismatch
		}
		session, err := h.taskSvc.GetTaskSession(ctx, sessionID)
		if err != nil || session == nil || session.TaskID != taskID || session.QueueIncarnationID == "" {
			return messagequeue.QueueSessionIdentity{}, messagequeue.ErrSessionIdentityMismatch
		}
		return messagequeue.QueueSessionIdentity{TaskID: taskID, SessionID: sessionID, SessionIncarnationID: session.QueueIncarnationID}, nil
	}
	resolver, ok := h.messageQueue.(queueIdentityResolver)
	if !ok {
		return messagequeue.QueueSessionIdentity{}, messagequeue.ErrSessionIdentityMismatch
	}
	identity, err := resolver.ResolveSessionIdentity(ctx, taskID, sessionID)
	if err != nil {
		return messagequeue.QueueSessionIdentity{}, err
	}
	return identity, nil
}

func queueAccessForbidden(msg *ws.Message) (messagequeue.QueueSessionIdentity, *ws.Message, error) {
	response, err := ws.NewError(msg.ID, msg.Action, ws.ErrorCodeForbidden, "message queue access is limited to the calling session", nil)
	return messagequeue.QueueSessionIdentity{}, response, err
}
