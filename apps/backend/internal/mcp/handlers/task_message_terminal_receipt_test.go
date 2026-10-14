package handlers

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	mcpscope "github.com/kandev/kandev/internal/mcp/scope"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository/repoerrors"
	"github.com/kandev/kandev/internal/task/service"
	ws "github.com/kandev/kandev/pkg/websocket"
	"github.com/stretchr/testify/require"
)

func TestTaskMessageAbsentTerminalOwnerRequiresRecovery(t *testing.T) {
	for _, state := range []models.TaskSessionState{
		models.TaskSessionStateFailed, models.TaskSessionStateCancelled, models.TaskSessionStateCompleted,
	} {
		t.Run(string(state), func(t *testing.T) {
			h, repo, ctx, req, notifier := taskMessageReceiptFixture(t)
			sessionID := req["session_id"].(string)
			before, err := repo.ListTurnsBySession(ctx, sessionID)
			require.NoError(t, err)
			require.NoError(t, repo.UpdateTaskSessionState(ctx, sessionID, state, "preserved failure"))
			for _, action := range []string{ws.ActionMCPGetTaskMessageStatus, ws.ActionMCPContinueTaskMessage, ws.ActionMCPMessageTask} {
				msg := makeWSMessage(t, action, req)
				var response *ws.Message
				if action == ws.ActionMCPMessageTask {
					response, err = h.handleMessageTask(ctx, msg)
				} else {
					response, err = h.handleGetTaskMessageStatus(ctx, msg)
				}
				require.NoError(t, err)
				r := messageReceiptResponse(t, response)
				require.Equal(t, "not_created", r.Status)
				require.False(t, r.RetryAllowed, "absence cannot authorize terminal-session admission")
				require.False(t, r.ContinueAllowed)
				require.Equal(t, "session_terminal", r.AdmissionError)
				require.Equal(t, "exact_session_recovery_required", r.Continuation)
				_, err = repo.GetMessage(ctx, r.IntentMessageID)
				require.Error(t, err)
			}
			after, err := repo.ListTurnsBySession(ctx, sessionID)
			require.NoError(t, err)
			require.Equal(t, before, after)
			session, err := repo.GetTaskSession(ctx, sessionID)
			require.NoError(t, err)
			require.Equal(t, state, session.State)
			require.Equal(t, "preserved failure", session.ErrorMessage)
			require.Zero(t, notifier.notifications)
			require.Empty(t, notifier.promptCalls)
			require.Empty(t, notifier.startCreatedCalls)
			require.Empty(t, notifier.turnStartCalls)
		})
	}
}

func TestTaskMessageTerminalReceiptKeepsOriginalIdentityAndFIFO(t *testing.T) {
	h, repo, ctx, req, notifier := taskMessageReceiptFixture(t)
	response, err := h.handleMessageTask(ctx, makeWSMessage(t, ws.ActionMCPMessageTask, req))
	require.NoError(t, err)
	original := messageReceiptResponse(t, response)
	require.Equal(t, "queued", original.Status)
	require.NoError(t, repo.UpdateTaskSessionState(ctx, req["session_id"].(string), models.TaskSessionStateFailed, "failed"))
	for _, action := range []string{ws.ActionMCPGetTaskMessageStatus, ws.ActionMCPContinueTaskMessage} {
		response, err = h.handleGetTaskMessageStatus(ctx, makeWSMessage(t, action, req))
		require.NoError(t, err)
		r := messageReceiptResponse(t, response)
		require.Equal(t, original.MessageID, r.MessageID)
		require.Equal(t, original.QueueID, r.QueueID)
		require.Equal(t, original.TurnID, r.TurnID)
		require.Equal(t, original.ContentSHA256, r.ContentSHA256)
		require.Equal(t, "queued", r.Status)
		require.False(t, r.RetryAllowed)
		require.False(t, r.ContinueAllowed)
		require.Equal(t, observeTaskMessageTurn, r.Continuation)
	}
	require.Equal(t, 1, notifier.notifications, "terminal receipt reads cannot notify the original queue")
}

func TestTaskMessageTerminalPreflightRechecksStateWithoutArmingSession(t *testing.T) {
	h, repo, ctx, req, notifier := taskMessageReceiptFixture(t)
	sessionID := req["session_id"].(string)
	require.NoError(t, repo.UpdateTaskSessionState(ctx, sessionID, models.TaskSessionStateFailed, "failed"))
	response, err := h.handleGetTaskMessageStatus(ctx, makeWSMessage(t, ws.ActionMCPGetTaskMessageStatus, req))
	require.NoError(t, err)
	require.False(t, messageReceiptResponse(t, response).RetryAllowed)
	// Only the fixture simulates a separately authorized lifecycle recovery.
	require.NoError(t, repo.UpdateTaskSessionState(ctx, sessionID, models.TaskSessionStateWaitingForInput, ""))
	response, err = h.handleGetTaskMessageStatus(ctx, makeWSMessage(t, ws.ActionMCPGetTaskMessageStatus, req))
	require.NoError(t, err)
	r := messageReceiptResponse(t, response)
	require.True(t, r.RetryAllowed)
	require.Empty(t, r.AdmissionError)
	require.Equal(t, "publish_same_intent", r.Continuation)
	require.Zero(t, notifier.notifications)
}

func TestTaskMessageTerminalPreflightPreservesUnknownDelivery(t *testing.T) {
	h, repo, ctx, req, notifier := taskMessageReceiptFixture(t)
	id := uuid.NewString()
	_, err := h.taskSvc.CreateMessageWithID(ctx, id, &service.CreateMessageRequest{
		TaskID: req["task_id"].(string), TaskSessionID: req["session_id"].(string),
		AuthorType: "user", Content: req["prompt"].(string),
		Metadata: map[string]any{"sender_task_id": req["sender_task_id"], "sender_session_id": "sender-sess-1"},
	})
	require.NoError(t, err)
	response, err := h.handleGetTaskMessageStatus(ctx, makeWSMessage(t, ws.ActionMCPGetTaskMessageStatus, req))
	require.NoError(t, err)
	original := messageReceiptResponse(t, response)
	require.Equal(t, "recorded_delivery_unknown", original.Status)
	require.NoError(t, repo.UpdateTaskSessionState(ctx, req["session_id"].(string), models.TaskSessionStateFailed, "failed"))
	response, err = h.handleMessageTask(ctx, makeWSMessage(t, ws.ActionMCPMessageTask, req))
	require.NoError(t, err)
	require.Equal(t, original, messageReceiptResponse(t, response), "terminal state cannot reinterpret an admitted original as absent")
	require.Zero(t, notifier.notifications)
}

func TestTaskMessageTerminalPreflightRejectsForeignCaller(t *testing.T) {
	h, repo, ctx, req, notifier := taskMessageReceiptFixture(t)
	require.NoError(t, repo.UpdateTaskSessionState(ctx, req["session_id"].(string), models.TaskSessionStateFailed, "failed"))
	for _, bad := range []context.Context{
		context.Background(),
		mcpscope.WithPrincipal(ctx, mcpscope.Principal{CallerTaskID: req["sender_task_id"].(string), CallerSessionID: "peer-session", WorkspaceID: "ws-1"}),
		mcpscope.WithPrincipal(ctx, mcpscope.Principal{CallerTaskID: req["sender_task_id"].(string), CallerSessionID: "sender-sess-1", WorkspaceID: "foreign"}),
	} {
		response, err := h.handleGetTaskMessageStatus(bad, makeWSMessage(t, ws.ActionMCPGetTaskMessageStatus, req))
		require.NoError(t, err)
		require.Equal(t, ws.MessageTypeError, response.Type)
	}
	require.Zero(t, notifier.notifications)
}

func TestTaskMessageAdmissionRaceRequiresStateCorrelation(t *testing.T) {
	r := &taskMessageReceipt{Status: "not_created", RetryAllowed: true, Continuation: "publish_same_intent"}
	applyTaskMessageAdmissionFailure(r, fmt.Errorf("atomic admission: %w", repoerrors.ErrTaskSessionUnavailable))
	require.Equal(t, "session_unavailable", r.AdmissionError)
	require.False(t, r.RetryAllowed)
	require.Equal(t, "correlate_session_state", r.Continuation)
	terminal := &taskMessageReceipt{Status: "not_created", AdmissionError: "session_terminal", Continuation: "exact_session_recovery_required"}
	applyTaskMessageAdmissionFailure(terminal, repoerrors.ErrTaskSessionUnavailable)
	require.Equal(t, "session_terminal", terminal.AdmissionError)
	require.Equal(t, "exact_session_recovery_required", terminal.Continuation)
}
