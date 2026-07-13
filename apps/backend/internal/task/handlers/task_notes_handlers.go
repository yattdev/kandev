package handlers

import (
	"context"
	"encoding/json"
	"errors"

	"go.uber.org/zap"

	"github.com/kandev/kandev/internal/events"
	"github.com/kandev/kandev/internal/events/bus"
	"github.com/kandev/kandev/internal/task/dto"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/service"
	ws "github.com/kandev/kandev/pkg/websocket"
)

const notesDocumentKey = "notes"

// notesDocumentService is the subset of DocumentService the notes handlers use.
type notesDocumentService interface {
	CreateOrUpdateDocument(ctx context.Context, taskID, key, docType, title, content, authorKind, authorName string) (*models.TaskDocument, error)
	GetDocument(ctx context.Context, taskID, key string) (*models.TaskDocument, error)
	DeleteDocument(ctx context.Context, taskID, key string) error
}

// notesEventPublisher is the subset of the event bus the notes handlers need.
type notesEventPublisher interface {
	Publish(ctx context.Context, subject string, event *bus.Event) error
}

// wsGetTaskNotes retrieves the notes document for a task.
// Returns null payload when no notes exist yet.
func (h *TaskHandlers) wsGetTaskNotes(ctx context.Context, msg *ws.Message) (*ws.Message, error) {
	var req struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeBadRequest, "Invalid payload: "+err.Error(), nil)
	}
	if req.TaskID == "" {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeValidation, "task_id is required", nil)
	}

	doc, err := h.documentService.GetDocument(ctx, req.TaskID, notesDocumentKey)
	if err != nil {
		if errors.Is(err, service.ErrDocumentNotFound) {
			return ws.NewResponse(msg.ID, msg.Action, nil)
		}
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeInternalError, "Failed to get task notes", nil)
	}

	return ws.NewResponse(msg.ID, msg.Action, dto.TaskNotesFromModel(doc))
}

// wsSaveTaskNotes creates or updates the notes document for a task.
func (h *TaskHandlers) wsSaveTaskNotes(ctx context.Context, msg *ws.Message) (*ws.Message, error) {
	var req struct {
		TaskID  string `json:"task_id"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeBadRequest, "Invalid payload: "+err.Error(), nil)
	}
	if req.TaskID == "" {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeValidation, "task_id is required", nil)
	}

	doc, err := h.documentService.CreateOrUpdateDocument(
		ctx, req.TaskID, notesDocumentKey, "notes", "Notes", req.Content, "user", "User",
	)
	if err != nil {
		if errors.Is(err, service.ErrDocumentTaskRequired) {
			return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeValidation, "task_id is required", nil)
		}
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeInternalError, "Failed to save task notes: "+err.Error(), nil)
	}

	h.publishNotesEvent(ctx, events.TaskNotesUpdated, dto.TaskNotesFromModel(doc))

	return ws.NewResponse(msg.ID, msg.Action, dto.TaskNotesFromModel(doc))
}

// wsDeleteTaskNotes removes the notes document for a task.
func (h *TaskHandlers) wsDeleteTaskNotes(ctx context.Context, msg *ws.Message) (*ws.Message, error) {
	var req struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeBadRequest, "Invalid payload: "+err.Error(), nil)
	}
	if req.TaskID == "" {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeValidation, "task_id is required", nil)
	}

	err := h.documentService.DeleteDocument(ctx, req.TaskID, notesDocumentKey)
	if err != nil {
		if errors.Is(err, service.ErrDocumentNotFound) {
			return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeNotFound, "Task notes not found", nil)
		}
		if errors.Is(err, service.ErrDocumentTaskRequired) {
			return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeValidation, "task_id is required", nil)
		}
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeInternalError, "Failed to delete task notes: "+err.Error(), nil)
	}

	h.publishNotesEvent(ctx, events.TaskNotesDeleted, map[string]string{"task_id": req.TaskID})

	return ws.NewResponse(msg.ID, msg.Action, map[string]interface{}{responseKeySuccess: true})
}

// publishNotesEvent publishes a notes event to the event bus. No-op if no bus is set.
func (h *TaskHandlers) publishNotesEvent(ctx context.Context, eventType string, payload interface{}) {
	if h.notesEventBus == nil {
		return
	}
	if err := h.notesEventBus.Publish(ctx, eventType, bus.NewEvent(eventType, "notes-handler", payload)); err != nil {
		h.logger.Warn("failed to publish notes event", zap.String("event_type", eventType), zap.Error(err))
	}
}
