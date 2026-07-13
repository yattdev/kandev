package handlers

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/common/logger"
	"github.com/kandev/kandev/internal/events/bus"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/service"
	ws "github.com/kandev/kandev/pkg/websocket"
	"go.uber.org/zap"
)

// stubDocumentService is a minimal in-memory stub for DocumentService-level calls.
type stubDocumentService struct {
	docs   map[string]*models.TaskDocument
	saveErr error
	getErr  error
	delErr  error
}

func newStubDocumentService() *stubDocumentService {
	return &stubDocumentService{docs: make(map[string]*models.TaskDocument)}
}

func (s *stubDocumentService) CreateOrUpdateDocument(
	_ context.Context,
	taskID, key, _, _, content, authorKind, authorName string,
) (*models.TaskDocument, error) {
	if s.saveErr != nil {
		return nil, s.saveErr
	}
	now := time.Now().UTC()
	existing := s.docs[taskID+":"+key]
	doc := &models.TaskDocument{
		ID:         "doc-1",
		TaskID:     taskID,
		Key:        key,
		Content:    content,
		AuthorKind: authorKind,
		AuthorName: authorName,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if existing != nil {
		doc.ID = existing.ID
		doc.CreatedAt = existing.CreatedAt
	}
	s.docs[taskID+":"+key] = doc
	return doc, nil
}

func (s *stubDocumentService) GetDocument(_ context.Context, taskID, key string) (*models.TaskDocument, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	doc := s.docs[taskID+":"+key]
	if doc == nil {
		return nil, service.ErrDocumentNotFound
	}
	return doc, nil
}

func (s *stubDocumentService) DeleteDocument(_ context.Context, taskID, key string) error {
	if s.delErr != nil {
		return s.delErr
	}
	k := taskID + ":" + key
	if s.docs[k] == nil {
		return service.ErrDocumentNotFound
	}
	delete(s.docs, k)
	return nil
}

// stubBus is a minimal event bus stub for tests (records published events).
type stubBusForNotes struct {
	published []string
}

func (b *stubBusForNotes) Publish(_ context.Context, subject string, _ *bus.Event) error {
	b.published = append(b.published, subject)
	return nil
}

func makeNotesHandlers(docSvc notesDocumentService, bus notesEventPublisher) *TaskHandlers {
	log := logger.NewLogger(zap.NewNop())
	h := &TaskHandlers{logger: log}
	h.documentService = docSvc
	h.notesEventBus = bus
	return h
}

func makeNotesMsg(action, payload string) *ws.Message {
	return &ws.Message{
		ID:      "msg-1",
		Action:  action,
		Payload: json.RawMessage(payload),
	}
}

func TestWsGetTaskNotes_NotFound(t *testing.T) {
	stub := newStubDocumentService()
	h := makeNotesHandlers(stub, nil)

	msg := makeNotesMsg("task.notes.get", `{"task_id":"task-1"}`)
	resp, err := h.wsGetTaskNotes(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Not found should return null payload (nil notes)
	if resp.Error != nil {
		t.Errorf("expected no error in response, got %v", resp.Error)
	}
}

func TestWsGetTaskNotes_Found(t *testing.T) {
	stub := newStubDocumentService()
	stub.docs["task-1:notes"] = &models.TaskDocument{
		ID:      "doc-1",
		TaskID:  "task-1",
		Key:     "notes",
		Content: "# My Notes",
	}
	h := makeNotesHandlers(stub, nil)

	msg := makeNotesMsg("task.notes.get", `{"task_id":"task-1"}`)
	resp, err := h.wsGetTaskNotes(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Error != nil {
		t.Errorf("expected no error in response, got %v", resp.Error)
	}
}

func TestWsGetTaskNotes_MissingTaskID(t *testing.T) {
	stub := newStubDocumentService()
	h := makeNotesHandlers(stub, nil)

	msg := makeNotesMsg("task.notes.get", `{}`)
	resp, err := h.wsGetTaskNotes(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Error == nil {
		t.Error("expected validation error for missing task_id")
	}
}

func TestWsSaveTaskNotes_Creates(t *testing.T) {
	stub := newStubDocumentService()
	bus := &stubBusForNotes{}
	h := makeNotesHandlers(stub, bus)

	msg := makeNotesMsg("task.notes.save", `{"task_id":"task-1","content":"hello notes"}`)
	resp, err := h.wsSaveTaskNotes(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Error != nil {
		t.Errorf("expected no error in response, got %v", resp.Error)
	}
	// Verify event published
	if len(bus.published) == 0 {
		t.Error("expected notes.updated event to be published")
	}
}

func TestWsSaveTaskNotes_MissingTaskID(t *testing.T) {
	stub := newStubDocumentService()
	h := makeNotesHandlers(stub, nil)

	msg := makeNotesMsg("task.notes.save", `{"content":"hello"}`)
	resp, err := h.wsSaveTaskNotes(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Error == nil {
		t.Error("expected validation error for missing task_id")
	}
}

func TestWsDeleteTaskNotes_Found(t *testing.T) {
	stub := newStubDocumentService()
	stub.docs["task-1:notes"] = &models.TaskDocument{
		ID:      "doc-1",
		TaskID:  "task-1",
		Key:     "notes",
		Content: "some notes",
	}
	bus := &stubBusForNotes{}
	h := makeNotesHandlers(stub, bus)

	msg := makeNotesMsg("task.notes.delete", `{"task_id":"task-1"}`)
	resp, err := h.wsDeleteTaskNotes(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Error != nil {
		t.Errorf("expected no error in response, got %v", resp.Error)
	}
	if len(bus.published) == 0 {
		t.Error("expected notes.deleted event to be published")
	}
}

func TestWsDeleteTaskNotes_NotFound(t *testing.T) {
	stub := newStubDocumentService()
	h := makeNotesHandlers(stub, nil)

	msg := makeNotesMsg("task.notes.delete", `{"task_id":"task-1"}`)
	resp, err := h.wsDeleteTaskNotes(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Error == nil {
		t.Error("expected not found error in response")
	}
}
