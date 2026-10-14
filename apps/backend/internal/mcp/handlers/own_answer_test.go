package handlers

import (
	"context"
	"encoding/json"
	"github.com/kandev/kandev/internal/clarification"
	mcpscope "github.com/kandev/kandev/internal/mcp/scope"
	ws "github.com/kandev/kandev/pkg/websocket"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestOwnAnswerAuthenticatedScopeAndNoChoiceInput(t *testing.T) {
	h, repo, ctx, q := ownQuestionFixture(t)
	payload := map[string]any{"task_id": "own-task", "session_id": "own-session", "question_id": q[0].ID, "pending_id": "pending", "question_message_id": "qm", "answer_message_id": "am", "question_sha256": strings.Repeat("a", 64), "answer_sha256": strings.Repeat("b", 64)}
	for _, caller := range []context.Context{context.Background(), mcpscope.WithPrincipal(ctx, mcpscope.Principal{CallerTaskID: "own-task", CallerSessionID: "own-session", WorkspaceID: "foreign"})} {
		response, err := h.handleReconcileOwnAnswer(caller, makeWSMessage(t, ws.ActionMCPReconcileOwnQuestionAnswer, payload))
		require.NoError(t, err)
		require.Equal(t, ws.MessageTypeError, response.Type)
		require.Contains(t, string(response.Payload), "exact own-task/session")
	}
	payload["answer"] = "fabricated"
	response, err := h.handleReconcileOwnAnswer(ctx, makeWSMessage(t, ws.ActionMCPReconcileOwnQuestionAnswer, payload))
	require.NoError(t, err)
	require.Equal(t, ws.MessageTypeError, response.Type)
	require.Contains(t, string(response.Payload), "identities/hashes")
	delete(payload, "answer")
	pending, _ := h.clarificationSvc.CreateRequest(&clarification.Request{PendingID: "pending", TaskID: "own-task", SessionID: "own-session", Questions: q})
	payload["pending_id"] = pending
	response, err = h.handleReconcileOwnAnswer(ctx, makeWSMessage(t, ws.ActionMCPReconcileOwnQuestionAnswer, payload))
	require.NoError(t, err)
	require.Equal(t, ws.MessageTypeError, response.Type)
	require.Contains(t, string(response.Payload), "live waiter")
	records, err := repo.FindOwnClarificationMessages(ctx, "own-task", "own-session", []string{q[0].ID})
	require.NoError(t, err)
	require.Empty(t, records)
}

func TestOwnAnswerReceiptReadback(t *testing.T) {
	h, repo, ctx, q := ownQuestionFixture(t)
	response, err := h.handleReceiptUserQuestion(ctx, makeWSMessage(t, ws.ActionMCPAskUserQuestion, map[string]any{}), "own-task", "own-session", q, "synthetic")
	require.NoError(t, err)
	r := ownReceipt(t, response)
	messages, err := repo.FindOwnClarificationMessages(ctx, "own-task", "own-session", []string{q[0].ID})
	require.NoError(t, err)
	m := messages[0]
	m.Metadata["status"] = "answered"
	m.Metadata["response"] = clarification.Answer{QuestionID: q[0].ID, SelectedOptions: []string{q[0].Options[0].ID}}
	m.Metadata["own_question_answer_receipt"] = map[string]any{"version": "own-question-answer/v1", "receipt_id": "synthetic-audit", "question_id": q[0].ID}
	require.NoError(t, repo.UpdateMessage(ctx, m))
	response, err = h.handleGetOwnQuestionStatus(ctx, makeWSMessage(t, ws.ActionMCPGetOwnQuestionStatus, map[string]any{"task_id": "own-task", "session_id": "own-session", "question_ids": []string{q[0].ID}}))
	require.NoError(t, err)
	settled := ownReceipt(t, response)
	require.Equal(t, "answered", settled.Status)
	require.Equal(t, r.PendingID, settled.PendingID)
	require.NotNil(t, settled.Questions[0].AnswerReceipt)
	raw, err := json.Marshal(settled)
	require.NoError(t, err)
	require.Contains(t, string(raw), "synthetic-audit")
}
