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
	sessions     map[string]*models.TaskSession
	receipt      *models.TaskSessionCleanupReceipt
	recoverQueue func(context.Context, messagequeue.QueueRecoveryScope) (*messagequeue.QueueRecoveryResult, error)
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

func (r *cleanupSessionRepo) RecoverTaskSessionQueue(
	ctx context.Context,
	scope messagequeue.QueueRecoveryScope,
) (*messagequeue.QueueRecoveryResult, error) {
	return r.recoverQueue(ctx, scope)
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
	queueRepository := messagequeue.NewMemoryRepository()
	queue := messagequeue.NewService(queueRepository, messagequeue.DefaultMaxPerSession, log)
	queue.SetAutoMergeEnabled(false)
	repo := &cleanupSessionRepo{sessions: map[string]*models.TaskSession{
		"caller": {ID: "caller", TaskID: "task-1", IsPrimary: true, State: models.TaskSessionStateWaitingForInput},
		"target": {ID: "target", TaskID: "task-1", State: models.TaskSessionStateCompleted},
		"other":  {ID: "other", TaskID: "task-2", State: models.TaskSessionStateCompleted},
	}}
	closer := &recordingSessionCloser{}
	repo.recoverQueue = func(ctx context.Context, scope messagequeue.QueueRecoveryScope) (*messagequeue.QueueRecoveryResult, error) {
		entries, err := queueRepository.RecoverSessionQueue(ctx, scope.SourceSessionID, scope.DestinationSessionID)
		if err != nil {
			return nil, err
		}
		return &messagequeue.QueueRecoveryResult{
			Receipt: messagequeue.QueueRecoveryReceipt{
				ID: "recovery-1", TaskID: scope.TaskID, WorkspaceID: scope.WorkspaceID,
				SourceSessionID: scope.SourceSessionID, DestinationSessionID: scope.DestinationSessionID,
				EntryCount: len(entries),
			},
			Entries: entries,
		}, nil
	}
	return &Handlers{sessionRepo: repo, queueManager: queue, sessionCloser: closer, logger: log}, queue, repo, closer
}

func TestRecoverSessionQueueReturnsExactFIFOToCurrentPrimary(t *testing.T) {
	h, queue, _, _ := cleanupHandlers(t)
	first, err := queue.QueueMessageWithMetadata(
		context.Background(), "target", "task-1", "first exact body", "model-a", messagequeue.QueuedByWorkflow, false, nil,
		map[string]interface{}{messagequeue.MetadataLifecycleDurable: true},
	)
	if err != nil {
		t.Fatalf("queue first: %v", err)
	}
	second, err := queue.QueueMessage(context.Background(), "target", "task-1", "second exact body", "model-b", messagequeue.QueuedByUser, true, nil)
	if err != nil {
		t.Fatalf("queue second: %v", err)
	}
	reserved, ok := queue.ReserveQueued(context.Background(), "target")
	if !ok || reserved == nil || reserved.ID != first.ID {
		t.Fatalf("reserve durable first = %#v, ok=%t", reserved, ok)
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
		Receipt messagequeue.QueueRecoveryReceipt `json:"receipt"`
		Entries []messagequeue.QueueRecoveryEntry `json:"entries"`
	}
	if err := resp.ParsePayload(&payload); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(payload.Entries) != 2 || payload.Entries[0].ID != first.ID || payload.Entries[1].ID != second.ID {
		t.Fatalf("FIFO entries = %#v", payload.Entries)
	}
	if payload.Receipt.ID != "recovery-1" || payload.Receipt.EntryCount != 2 ||
		payload.Receipt.SourceSessionID != "target" || payload.Receipt.DestinationSessionID != "caller" {
		t.Fatalf("recovery receipt = %#v", payload.Receipt)
	}
	if payload.Entries[0].Content != "first exact body" || payload.Entries[1].Content != "second exact body" {
		t.Fatalf("exact bodies = %#v", payload.Entries)
	}
	if payload.Entries[0].ContentSHA256 != "b8c1a59e8cee69b502e3411acb6090e2f13460f0681191b6e68ca5e13b753623" ||
		payload.Entries[1].ContentSHA256 != "4a4d5c998b5977a1daa3bccac736b92f22af36e5e467e31f909fa7bd88c39f8e" {
		t.Fatalf("exact readback hashes = %#v", payload.Entries)
	}
	if !payload.Entries[0].ReservedInFlight {
		t.Fatalf("reserved source state missing from readback: %#v", payload.Entries[0])
	}
	if source := queue.GetStatus(context.Background(), "target"); source.Count != 0 {
		t.Fatalf("source queue after recovery = %#v", source)
	}
	destination := queue.GetStatus(context.Background(), "caller")
	if destination.Count != 2 || destination.Entries[0].ID != first.ID || destination.Entries[1].ID != second.ID {
		t.Fatalf("destination FIFO after recovery = %#v", destination)
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

func TestRecoverSessionQueueRejectsCallerThatLostPrimaryAfterPreflight(t *testing.T) {
	h, queue, repo, _ := cleanupHandlers(t)
	queued, err := queue.QueueMessage(context.Background(), "target", "task-1", "must remain", "", messagequeue.QueuedByUser, false, nil)
	if err != nil {
		t.Fatalf("queue target entry: %v", err)
	}
	repo.recoverQueue = func(context.Context, messagequeue.QueueRecoveryScope) (*messagequeue.QueueRecoveryResult, error) {
		repo.sessions["caller"].IsPrimary = false
		return nil, messagequeue.ErrQueueRecoveryUnauthorized
	}

	resp, err := h.handleRecoverSessionQueue(
		queuePrincipal("workspace-1", "task-1", "caller"),
		makeWSMessage(t, ws.ActionMCPRecoverSessionQueue, map[string]interface{}{
			"task_id": "task-1", "caller_session_id": "caller", "target_session_id": "target",
		}),
	)
	if err != nil {
		t.Fatalf("recover stale primary: %v", err)
	}
	assertWSError(t, resp, ws.ErrorCodeForbidden)
	status := queue.GetStatus(context.Background(), "target")
	if status.Count != 1 || status.Entries[0].ID != queued.ID {
		t.Fatalf("target queue changed after stale authorization: %#v", status)
	}
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
