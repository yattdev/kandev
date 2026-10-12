package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	mcpprofile "github.com/kandev/kandev/internal/mcp/profile"
	mcpscope "github.com/kandev/kandev/internal/mcp/scope"
	"github.com/kandev/kandev/internal/orchestrator/messagequeue"
	"github.com/kandev/kandev/internal/task/models"
	sqliterepo "github.com/kandev/kandev/internal/task/repository/sqlite"
	"github.com/kandev/kandev/internal/task/service"
	v1 "github.com/kandev/kandev/pkg/api/v1"
	ws "github.com/kandev/kandev/pkg/websocket"
	"github.com/stretchr/testify/require"
)

type receiptNotifier struct {
	*fakeOrchestrator
	notifications int
}

func (n *receiptNotifier) NotifyQueuedUserPrompt(context.Context, string, string) {
	n.mu.Lock()
	n.notifications++
	n.mu.Unlock()
}

func taskMessageReceiptFixture(t *testing.T) (*Handlers, *sqliterepo.Repository, context.Context, map[string]any, *receiptNotifier) {
	t.Helper()
	svc, repo := newTestTaskService(t)
	sender, target, session := seedTaskWithSession(t, svc, repo, models.TaskSessionStateWaitingForInput)
	require.NoError(t, repo.UpdateTaskState(context.Background(), target.ID, v1.TaskStateReview))
	pool := sqlx.NewDb(repo.DB(), "sqlite3")
	queueRepo, err := messagequeue.NewSQLiteRepository(pool, pool)
	require.NoError(t, err)
	notifier := &receiptNotifier{fakeOrchestrator: &fakeOrchestrator{queue: messagequeue.NewService(queueRepo, 10, testLogger(t))}}
	h, _ := newMessageTaskHandler(t, svc, repo)
	h.sessionLauncher = notifier
	ctx := mcpscope.WithPrincipal(context.Background(), mcpscope.Principal{CallerTaskID: sender.ID, CallerSessionID: "sender-sess-1", WorkspaceID: sender.WorkspaceID, Surface: mcpprofile.SurfaceKanbanTask})
	req := map[string]any{"task_id": target.ID, "session_id": session.ID, "sender_task_id": sender.ID, "sender_session_id": "sender-sess-1", "intent_id": "synthetic-intent-0001", "prompt": "synthetic-intent-0001\nHarmless fixture message", "delivery_mode": "queued"}
	return h, repo, ctx, req, notifier
}

func messageReceiptResponse(t *testing.T, response *ws.Message) taskMessageReceipt {
	t.Helper()
	require.Equal(t, ws.MessageTypeResponse, response.Type, string(response.Payload))
	var r taskMessageReceipt
	require.NoError(t, json.Unmarshal(response.Payload, &r))
	return r
}

func TestTaskMessageReceiptAtomicReplayAndContinuation(t *testing.T) {
	h, repo, ctx, req, notifier := taskMessageReceiptFixture(t)
	response, err := h.handleGetTaskMessageStatus(ctx, makeWSMessage(t, ws.ActionMCPGetTaskMessageStatus, req))
	require.NoError(t, err)
	absent := messageReceiptResponse(t, response)
	require.Equal(t, "not_created", absent.Status)
	require.True(t, absent.RetryAllowed)
	response, err = h.handleMessageTask(ctx, makeWSMessage(t, ws.ActionMCPMessageTask, req))
	require.NoError(t, err)
	first := messageReceiptResponse(t, response)
	require.Equal(t, "queued", first.Status)
	require.Equal(t, first.IntentMessageID, first.MessageID)
	require.Equal(t, first.MessageID, first.QueueID)
	require.False(t, first.RetryAllowed)
	require.Empty(t, notifier.promptCalls)
	require.Empty(t, notifier.startCreatedCalls)
	require.Empty(t, notifier.turnStartCalls)
	task, err := repo.GetTask(ctx, req["task_id"].(string))
	require.NoError(t, err)
	require.Equal(t, v1.TaskStateReview, task.State, "admission does not move the QA owner")
	for i := 0; i < 3; i++ {
		response, err = h.handleMessageTask(ctx, makeWSMessage(t, ws.ActionMCPMessageTask, req))
		require.NoError(t, err)
		require.Equal(t, first, messageReceiptResponse(t, response))
	}
	require.Equal(t, 1, notifier.notifications, "replay cannot reapply readiness/moves")
	response, err = h.handleGetTaskMessageStatus(ctx, makeWSMessage(t, ws.ActionMCPContinueTaskMessage, req))
	require.NoError(t, err)
	require.Equal(t, first, messageReceiptResponse(t, response))
	require.Equal(t, 2, notifier.notifications)
	require.NoError(t, repo.UpdateTaskSessionState(ctx, req["session_id"].(string), models.TaskSessionStateCancelled, ""))
	response, err = h.handleGetTaskMessageStatus(ctx, makeWSMessage(t, ws.ActionMCPGetTaskMessageStatus, req))
	require.NoError(t, err)
	require.False(t, messageReceiptResponse(t, response).ContinueAllowed, "cancelled owners cannot be woken")
	records, err := repo.FindOwnTaskMessageRecords(ctx, "ws-1", req["sender_task_id"].(string), "sender-sess-1", task.ID, req["session_id"].(string), req["intent_id"].(string), "")
	require.NoError(t, err)
	require.Len(t, records, 2)
	req["prompt"] = "Changed message under original identity"
	response, err = h.handleMessageTask(ctx, makeWSMessage(t, ws.ActionMCPMessageTask, req))
	require.NoError(t, err)
	require.Equal(t, ws.MessageTypeError, response.Type)
}

func TestTaskMessageReceiptLegacyUnknownAndScope(t *testing.T) {
	h, _, ctx, req, notifier := taskMessageReceiptFixture(t)
	id := uuid.NewString()
	metadata := map[string]any{"sender_task_id": req["sender_task_id"], "sender_session_id": "sender-sess-1"}
	_, err := h.taskSvc.CreateMessageWithID(ctx, id, &service.CreateMessageRequest{TaskID: req["task_id"].(string), TaskSessionID: req["session_id"].(string), AuthorType: "user", Content: req["prompt"].(string), Metadata: metadata})
	require.NoError(t, err)
	response, err := h.handleGetTaskMessageStatus(ctx, makeWSMessage(t, ws.ActionMCPGetTaskMessageStatus, req))
	require.NoError(t, err)
	original := messageReceiptResponse(t, response)
	require.Equal(t, "recorded_delivery_unknown", original.Status)
	require.Equal(t, id, original.MessageID)
	require.False(t, original.RetryAllowed)
	require.False(t, original.ContinueAllowed)
	missingUUID := map[string]any{"sender_task_id": req["sender_task_id"], "sender_session_id": "sender-sess-1", "message_id": uuid.NewString()}
	response, err = h.handleGetTaskMessageStatus(ctx, makeWSMessage(t, ws.ActionMCPGetTaskMessageStatus, missingUUID))
	require.NoError(t, err)
	missing := messageReceiptResponse(t, response)
	require.Equal(t, "not_found", missing.Status)
	require.False(t, missing.RetryAllowed)
	caseMismatch := map[string]any{"task_id": req["task_id"], "session_id": req["session_id"], "sender_task_id": req["sender_task_id"], "sender_session_id": "sender-sess-1", "intent_id": strings.ToUpper(req["intent_id"].(string))}
	response, err = h.handleGetTaskMessageStatus(ctx, makeWSMessage(t, ws.ActionMCPGetTaskMessageStatus, caseMismatch))
	require.NoError(t, err)
	require.Equal(t, "not_created", messageReceiptResponse(t, response).Status, "legacy correlation is byte-exact")
	lookup := map[string]any{"sender_task_id": req["sender_task_id"], "sender_session_id": "sender-sess-1", "message_id": id}
	response, err = h.handleGetTaskMessageStatus(ctx, makeWSMessage(t, ws.ActionMCPContinueTaskMessage, lookup))
	require.NoError(t, err)
	require.Equal(t, id, messageReceiptResponse(t, response).MessageID)
	require.Zero(t, notifier.notifications)
	response, err = h.handleMessageTask(ctx, makeWSMessage(t, ws.ActionMCPMessageTask, req))
	require.NoError(t, err)
	require.Equal(t, id, messageReceiptResponse(t, response).MessageID)
	for _, bad := range []context.Context{context.Background(), mcpscope.WithPrincipal(ctx, mcpscope.Principal{CallerTaskID: req["sender_task_id"].(string), CallerSessionID: "peer-session", WorkspaceID: "ws-1"}), mcpscope.WithPrincipal(ctx, mcpscope.Principal{CallerTaskID: req["sender_task_id"].(string), CallerSessionID: "sender-sess-1", WorkspaceID: "foreign"})} {
		response, err = h.handleGetTaskMessageStatus(bad, makeWSMessage(t, ws.ActionMCPGetTaskMessageStatus, lookup))
		require.NoError(t, err)
		require.Equal(t, ws.MessageTypeError, response.Type)
	}
	duplicate := &models.Message{ID: uuid.NewString(), TaskID: req["task_id"].(string), TaskSessionID: req["session_id"].(string), Content: req["prompt"].(string), Type: models.MessageTypeMessage, AuthorType: models.MessageAuthorUser, Metadata: metadata, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	_, err = h.taskSvc.CreateMessageWithID(ctx, duplicate.ID, &service.CreateMessageRequest{TaskID: duplicate.TaskID, TaskSessionID: duplicate.TaskSessionID, Content: duplicate.Content, AuthorType: "user", Metadata: duplicate.Metadata})
	require.NoError(t, err)
	response, err = h.handleGetTaskMessageStatus(ctx, makeWSMessage(t, ws.ActionMCPGetTaskMessageStatus, req))
	require.NoError(t, err)
	require.Equal(t, ws.MessageTypeError, response.Type, "multiple legacy identities fail closed")
}

func TestTaskMessageReceiptConcurrentTimeoutRetry(t *testing.T) {
	h, repo, ctx, req, _ := taskMessageReceiptFixture(t)
	var wg sync.WaitGroup
	results := make(chan *ws.Message, 4)
	msg := makeWSMessage(t, ws.ActionMCPMessageTask, req)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			response, err := h.handleMessageTask(ctx, msg)
			if err != nil {
				return
			}
			results <- response
		}()
	}
	wg.Wait()
	close(results)
	var identity string
	for response := range results {
		r := messageReceiptResponse(t, response)
		if identity == "" {
			identity = r.MessageID
		}
		require.Equal(t, identity, r.MessageID)
	}
	records, err := repo.FindOwnTaskMessageRecords(ctx, "ws-1", req["sender_task_id"].(string), "sender-sess-1", req["task_id"].(string), req["session_id"].(string), req["intent_id"].(string), "")
	require.NoError(t, err)
	require.Len(t, records, 2)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	response, err := h.handleMessageTask(cancelled, msg)
	require.NoError(t, err)
	require.Equal(t, ws.MessageTypeError, response.Type)
	response, err = h.handleGetTaskMessageStatus(ctx, makeWSMessage(t, ws.ActionMCPGetTaskMessageStatus, req))
	require.NoError(t, err)
	require.Equal(t, identity, messageReceiptResponse(t, response).MessageID)
}

func TestTaskMessageWriteBoundedLockRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		err := retryTaskMessageWrite(context.Background(), func() error {
			calls++
			if calls < 3 {
				return errors.New("database is locked")
			}
			return nil
		})
		require.NoError(t, err)
		require.Equal(t, 3, calls)
		calls = 0
		err = retryTaskMessageWrite(context.Background(), func() error { calls++; return errors.New("database is locked") })
		require.Error(t, err)
		require.Equal(t, 3, calls)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		calls = 0
		err = retryTaskMessageWrite(ctx, func() error { calls++; return errors.New("database is locked") })
		require.ErrorIs(t, err, context.Canceled)
		require.Equal(t, 1, calls)
	})
}

func TestTaskMessageReceiptFullQueueAndTerminalAdmissionPreserveState(t *testing.T) {
	h, repo, ctx, req, notifier := taskMessageReceiptFixture(t)
	notifier.queue.SetMaxPerSession(1)
	_, err := notifier.queue.QueueMessageWithMetadata(ctx, req["session_id"].(string), req["task_id"].(string), "existing FIFO sentinel", "", messagequeue.QueuedByUser, false, nil, nil)
	require.NoError(t, err)
	response, err := h.handleMessageTask(ctx, makeWSMessage(t, ws.ActionMCPMessageTask, req))
	require.NoError(t, err)
	denied := messageReceiptResponse(t, response)
	require.Equal(t, "not_created", denied.Status)
	require.True(t, denied.RetryAllowed)
	require.Equal(t, "admission_unavailable", denied.AdmissionError)
	_, err = repo.GetMessage(ctx, denied.IntentMessageID)
	require.Error(t, err, "failed queue transaction cannot leave a transcript")
	require.NoError(t, repo.UpdateTaskSessionState(ctx, req["session_id"].(string), models.TaskSessionStateCancelled, ""))
	response, err = h.handleMessageTask(ctx, makeWSMessage(t, ws.ActionMCPMessageTask, req))
	require.NoError(t, err)
	require.Equal(t, "not_created", messageReceiptResponse(t, response).Status)
	task, err := repo.GetTask(ctx, req["task_id"].(string))
	require.NoError(t, err)
	require.Equal(t, v1.TaskStateReview, task.State)
	require.Empty(t, notifier.promptCalls)
	require.Empty(t, notifier.startCreatedCalls)
}
