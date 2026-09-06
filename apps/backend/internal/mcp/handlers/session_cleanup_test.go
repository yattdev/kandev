package handlers

import (
	"context"
	"errors"
	"testing"

	"github.com/kandev/kandev/internal/common/logger"
	"github.com/kandev/kandev/internal/orchestrator/messagequeue"
	"github.com/kandev/kandev/internal/task/models"
	ws "github.com/kandev/kandev/pkg/websocket"
)

type cleanupSessionRepo struct {
	SessionRepository
	sessions map[string]*models.TaskSession
	receipt  *models.TaskSessionCleanupReceipt
}

func (r *cleanupSessionRepo) GetTaskSession(_ context.Context, id string) (*models.TaskSession, error) {
	if session := r.sessions[id]; session != nil {
		copy := *session
		return &copy, nil
	}
	return nil, errors.New("not found")
}

func (r *cleanupSessionRepo) GetTaskSessionCleanupReceipt(_ context.Context, taskID, sessionID string) (*models.TaskSessionCleanupReceipt, error) {
	if r.receipt != nil && r.receipt.TaskID == taskID && r.receipt.SessionID == sessionID {
		copy := *r.receipt
		return &copy, nil
	}
	return nil, errors.New("not found")
}

type recordingSessionCloser struct {
	called string
	err    error
	after  func()
}

func (c *recordingSessionCloser) DeleteSession(_ context.Context, sessionID string) error {
	c.called = sessionID
	if c.after != nil {
		c.after()
	}
	return c.err
}

func cleanupHandlers(t *testing.T) (*Handlers, *messagequeue.Service, *cleanupSessionRepo, *recordingSessionCloser) {
	t.Helper()
	log, err := logger.NewLogger(logger.LoggingConfig{Level: "error", Format: "console", OutputPath: "stderr"})
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	queue := messagequeue.NewServiceMemory(log)
	queue.SetAutoMergeEnabled(false)
	repo := &cleanupSessionRepo{sessions: map[string]*models.TaskSession{
		"caller": {ID: "caller", TaskID: "task-1", IsPrimary: true, State: models.TaskSessionStateWaitingForInput},
		"target": {ID: "target", TaskID: "task-1", State: models.TaskSessionStateCompleted},
		"other":  {ID: "other", TaskID: "task-2", State: models.TaskSessionStateCompleted},
	}}
	closer := &recordingSessionCloser{}
	return &Handlers{sessionRepo: repo, queueManager: queue, sessionCloser: closer, logger: log}, queue, repo, closer
}

func TestRecoverSessionQueueReturnsExactFIFOToCurrentPrimary(t *testing.T) {
	h, queue, _, _ := cleanupHandlers(t)
	first, err := queue.QueueMessage(context.Background(), "target", "task-1", "first exact body", "model-a", messagequeue.QueuedByAgent, false, nil)
	if err != nil {
		t.Fatalf("queue first: %v", err)
	}
	second, err := queue.QueueMessage(context.Background(), "target", "task-1", "second exact body", "model-b", messagequeue.QueuedByUser, true, nil)
	if err != nil {
		t.Fatalf("queue second: %v", err)
	}
	resp, err := h.handleRecoverSessionQueue(
		queuePrincipal("workspace-1", "task-1", "caller"),
		makeWSMessage(t, ws.ActionMCPRecoverSessionQueue, map[string]interface{}{
			"task_id": "task-1", "caller_session_id": "caller", "target_session_id": "target",
		}),
	)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	var payload struct {
		Entries []messagequeue.QueueRecoveryEntry `json:"entries"`
	}
	if err := resp.ParsePayload(&payload); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(payload.Entries) != 2 || payload.Entries[0].ID != first.ID || payload.Entries[1].ID != second.ID {
		t.Fatalf("FIFO entries = %#v", payload.Entries)
	}
	if payload.Entries[0].Content != "first exact body" || payload.Entries[1].Content != "second exact body" {
		t.Fatalf("exact bodies = %#v", payload.Entries)
	}
	if payload.Entries[0].ContentSHA256 != "b8c1a59e8cee69b502e3411acb6090e2f13460f0681191b6e68ca5e13b753623" ||
		payload.Entries[1].ContentSHA256 != "4a4d5c998b5977a1daa3bccac736b92f22af36e5e467e31f909fa7bd88c39f8e" {
		t.Fatalf("exact readback hashes = %#v", payload.Entries)
	}
}

func TestRecoverSessionQueueRejectsCrossTaskAndNonPrimaryCallers(t *testing.T) {
	h, _, repo, _ := cleanupHandlers(t)
	for name, target := range map[string]string{"cross task": "other", "current primary": "caller"} {
		t.Run(name, func(t *testing.T) {
			resp, err := h.handleRecoverSessionQueue(
				queuePrincipal("workspace-1", "task-1", "caller"),
				makeWSMessage(t, ws.ActionMCPRecoverSessionQueue, map[string]interface{}{
					"task_id": "task-1", "caller_session_id": "caller", "target_session_id": target,
				}),
			)
			if err != nil {
				t.Fatalf("recover: %v", err)
			}
			assertWSError(t, resp, ws.ErrorCodeForbidden)
		})
	}
	repo.sessions["caller"].IsPrimary = false
	resp, err := h.handleRecoverSessionQueue(
		queuePrincipal("workspace-1", "task-1", "caller"),
		makeWSMessage(t, ws.ActionMCPRecoverSessionQueue, map[string]interface{}{
			"task_id": "task-1", "caller_session_id": "caller", "target_session_id": "target",
		}),
	)
	if err != nil {
		t.Fatalf("recover non-primary: %v", err)
	}
	assertWSError(t, resp, ws.ErrorCodeForbidden)
}

func TestCloseTaskSessionIsScopedAndReturnsDurableReceipt(t *testing.T) {
	h, _, repo, closer := cleanupHandlers(t)
	closer.after = func() {
		repo.receipt = &models.TaskSessionCleanupReceipt{
			TaskID: "task-1", WorkspaceID: "workspace-1", SessionID: "target", Disposition: "archived", EvidenceRetained: true,
		}
	}
	resp, err := h.handleCloseTaskSession(
		queuePrincipal("workspace-1", "task-1", "caller"),
		makeWSMessage(t, ws.ActionMCPCloseTaskSession, map[string]interface{}{
			"task_id": "task-1", "caller_session_id": "caller", "target_session_id": "target",
		}),
	)
	if err != nil {
		t.Fatalf("close: %v", err)
	}
	if closer.called != "target" {
		t.Fatalf("DeleteSession called with %q", closer.called)
	}
	var payload struct {
		Receipt *models.TaskSessionCleanupReceipt `json:"receipt"`
	}
	if err := resp.ParsePayload(&payload); err != nil || payload.Receipt == nil || payload.Receipt.Disposition != "archived" {
		t.Fatalf("receipt payload = %#v, err=%v", payload, err)
	}

	closer.called = ""
	resp, err = h.handleCloseTaskSession(
		queuePrincipal("workspace-1", "task-1", "caller"),
		makeWSMessage(t, ws.ActionMCPCloseTaskSession, map[string]interface{}{
			"task_id": "task-1", "caller_session_id": "caller", "target_session_id": "other",
		}),
	)
	if err != nil {
		t.Fatalf("cross task close: %v", err)
	}
	assertWSError(t, resp, ws.ErrorCodeForbidden)
	if closer.called != "" {
		t.Fatalf("cross-task close reached closer with %q", closer.called)
	}
}

func TestCloseTaskSessionRetryAfterHardDeleteReturnsSameReceipt(t *testing.T) {
	h, _, repo, closer := cleanupHandlers(t)
	delete(repo.sessions, "target")
	repo.receipt = &models.TaskSessionCleanupReceipt{
		TaskID: "task-1", WorkspaceID: "workspace-1", SessionID: "target", Disposition: "deleted",
	}
	resp, err := h.handleCloseTaskSession(
		queuePrincipal("workspace-1", "task-1", "caller"),
		makeWSMessage(t, ws.ActionMCPCloseTaskSession, map[string]interface{}{
			"task_id": "task-1", "caller_session_id": "caller", "target_session_id": "target",
		}),
	)
	if err != nil {
		t.Fatalf("retry close: %v", err)
	}
	if closer.called != "" {
		t.Fatalf("idempotent retry called closer with %q", closer.called)
	}
	var payload struct {
		Receipt *models.TaskSessionCleanupReceipt `json:"receipt"`
	}
	if err := resp.ParsePayload(&payload); err != nil || payload.Receipt == nil || payload.Receipt.Disposition != "deleted" {
		t.Fatalf("retry receipt = %#v, err=%v", payload, err)
	}
}
