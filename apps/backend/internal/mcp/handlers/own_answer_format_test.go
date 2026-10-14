package handlers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/kandev/kandev/internal/orchestrator/messagequeue"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/ownanswer"
	sqliterepo "github.com/kandev/kandev/internal/task/repository/sqlite"
	ws "github.com/kandev/kandev/pkg/websocket"
	"github.com/stretchr/testify/require"
)

func nativeCommentsFixture(t *testing.T) (*Handlers, *sqliterepo.Repository, context.Context, map[string]any) {
	t.Helper()
	h, repo, ctx, questions := ownQuestionFixture(t)
	response, err := h.handleReceiptUserQuestion(ctx, makeWSMessage(t, ws.ActionMCPAskUserQuestion, map[string]any{}), "own-task", "own-session", questions, "synthetic")
	require.NoError(t, err)
	receipt := ownReceipt(t, response)
	q, err := repo.GetMessage(ctx, receipt.Questions[0].MessageID)
	require.NoError(t, err)
	q.Metadata["agent_disconnected"] = true
	require.NoError(t, repo.UpdateMessage(ctx, q))
	am := &models.Message{ID: uuid.NewString(), TaskID: q.TaskID, TaskSessionID: q.TaskSessionID, TurnID: q.TurnID, AuthorType: models.MessageAuthorUser, Type: models.MessageTypeMessage,
		Content:   "### Agent Message Comments\n\nSelected agent response:\n> Synthetic choice?\n\nUser feedback:\n> Monitor four only.\n> Keep qualifications.\n\n---\n",
		CreatedAt: q.CreatedAt.Add(time.Second), UpdatedAt: q.CreatedAt.Add(time.Second),
		Metadata: map[string]any{"client_message_request_fingerprint": strings.Repeat("a", 64), "queue_admission_ids": []string{uuid.NewString()}}}
	require.NoError(t, repo.CreateMessage(ctx, am))
	pool := sqlx.NewDb(repo.DB(), "sqlite3") // same owned test pool, no second connection/close
	_, err = messagequeue.NewSQLiteRepository(pool, pool)
	require.NoError(t, err)
	session, err := repo.GetTaskSession(ctx, q.TaskSessionID)
	require.NoError(t, err)
	ids := am.Metadata["queue_admission_ids"].([]string)
	queued := messagequeue.QueuedMessage{ID: ids[0], TaskID: q.TaskID, SessionID: q.TaskSessionID, Content: am.Content, QueuedBy: messagequeue.QueuedByUser, Metadata: map[string]any{"queue_admission_ids": ids}, QueuedAt: q.CreatedAt}
	raw, err := json.Marshal(queued)
	require.NoError(t, err)
	_, err = repo.DB().ExecContext(ctx, `INSERT INTO queue_admission_receipts(task_id,session_id,session_incarnation_id,client_queue_id,request_fingerprint,accepted_queue_id,response_json,admitted_at) VALUES (?,?,?,?,?,?,?,?)`, q.TaskID, q.TaskSessionID, session.QueueIncarnationID, ids[0], strings.Repeat("b", 64), ids[0], string(raw), queued.QueuedAt)
	require.NoError(t, err)
	raw, err = json.Marshal(q.Metadata["question"])
	require.NoError(t, err)
	payload := map[string]any{"result_mode": "explicit_disposition_v1", "task_id": q.TaskID, "session_id": q.TaskSessionID, "question_id": questions[0].ID, "pending_id": receipt.PendingID, "question_message_id": q.ID, "answer_message_id": am.ID, "question_sha256": ownanswer.OwnAnswerDigest(raw), "answer_sha256": ownanswer.OwnAnswerDigest([]byte(am.Content))}
	return h, repo, ctx, payload
}

func TestOwnAnswerUnsupportedFormatReturnsGuardedDisposition(t *testing.T) {
	h, repo, ctx, payload := nativeCommentsFixture(t)
	before, err := repo.GetMessage(ctx, payload["question_message_id"].(string))
	require.NoError(t, err)
	human, err := repo.GetMessage(ctx, payload["answer_message_id"].(string))
	require.NoError(t, err)
	response, err := h.handleReconcileOwnAnswer(ctx, makeWSMessage(t, ws.ActionMCPReconcileOwnQuestionAnswer, payload))
	require.NoError(t, err)
	require.Equal(t, ws.MessageTypeResponse, response.Type)
	var result map[string]any
	require.NoError(t, json.Unmarshal(response.Payload, &result))
	require.Equal(t, "not_reconciled", result["status"])
	require.Equal(t, "ANSWER_FORMAT_UNSUPPORTED", result["reason_code"])
	require.Equal(t, false, result["retry_allowed"])
	require.Equal(t, false, result["changed"])
	require.Equal(t, "pending", result["question_status"])
	disposition, ok := result["disposition"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "human_confirmed_original_rejection", disposition["kind"])
	require.Equal(t, true, disposition["requires_human_confirmation"])
	require.Equal(t, false, disposition["runtime_resume"])
	require.Equal(t, false, disposition["queue_replay"])
	require.Equal(t, "POST", disposition["method"])
	require.Equal(t, "/api/v1/clarification/"+payload["pending_id"].(string)+"/respond", disposition["path"])
	raw, err := json.Marshal(disposition["body"])
	require.NoError(t, err)
	require.JSONEq(t, `{"answers":[],"rejected":true}`, string(raw))
	q, err := repo.GetMessage(ctx, before.ID)
	require.NoError(t, err)
	a, err := repo.GetMessage(ctx, human.ID)
	require.NoError(t, err)
	require.Equal(t, before, q)
	require.Equal(t, human, a)
	_, err = repo.DB().ExecContext(ctx, `DELETE FROM queue_admission_receipts WHERE task_id=? AND session_id=?`, before.TaskID, before.TaskSessionID)
	require.NoError(t, err)
	response, err = h.handleReconcileOwnAnswer(ctx, makeWSMessage(t, ws.ActionMCPReconcileOwnQuestionAnswer, payload))
	require.NoError(t, err)
	require.Equal(t, ws.MessageTypeError, response.Type)
	require.NotContains(t, string(response.Payload), "ANSWER_FORMAT_UNSUPPORTED")
}

// Old running sidecars must continue to receive an error, rather than treating
// a structured refusal as a successful answer reconciliation.
func TestOwnAnswerUnsupportedFormatLegacyClientStillFailsClosed(t *testing.T) {
	h, _, ctx, payload := nativeCommentsFixture(t)
	delete(payload, "result_mode")
	response, err := h.handleReconcileOwnAnswer(ctx, makeWSMessage(t, ws.ActionMCPReconcileOwnQuestionAnswer, payload))
	require.NoError(t, err)
	require.Equal(t, ws.MessageTypeError, response.Type)
	require.NotContains(t, string(response.Payload), "human_confirmed_original_rejection")
}
