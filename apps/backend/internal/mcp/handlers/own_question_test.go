package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/clarification"
	mcpprofile "github.com/kandev/kandev/internal/mcp/profile"
	mcpscope "github.com/kandev/kandev/internal/mcp/scope"
	"github.com/kandev/kandev/internal/task/models"
	sqliterepo "github.com/kandev/kandev/internal/task/repository/sqlite"
	ws "github.com/kandev/kandev/pkg/websocket"
	"github.com/stretchr/testify/require"
)

type ownQuestionCreator struct {
	repo *sqliterepo.Repository
	fail bool
}

func (c *ownQuestionCreator) CreateClarificationRequestMessages(ctx context.Context, taskID, sessionID, pending string, questions []clarification.Question, questionContext string) ([]string, error) {
	if c.fail {
		return nil, errors.New("synthetic publication failure")
	}
	ids := []string{}
	for _, q := range questions {
		now := time.Now().UTC()
		m := &models.Message{TaskID: taskID, TaskSessionID: sessionID, TurnID: "own-turn", AuthorType: "agent", Content: "synthetic prompt", Type: models.MessageTypeClarificationRequest, RequestsInput: true, CreatedAt: now, UpdatedAt: now,
			Metadata: map[string]any{"question_id": q.ID, "pending_id": pending, "status": "pending", "question": q, "context": questionContext}}
		if err := c.repo.CreateMessage(ctx, m); err != nil {
			return nil, err
		}
		ids = append(ids, m.ID)
	}
	return ids, nil
}

func ownQuestionFixture(t *testing.T) (*Handlers, *sqliterepo.Repository, context.Context, []clarification.Question) {
	t.Helper()
	svc, repo := newTestTaskService(t)
	seedMCPHandlerSession(t, repo, "own-task", "own-session", models.TaskSessionStateRunning)
	require.NoError(t, repo.CreateTurn(context.Background(), &models.Turn{ID: "own-turn", TaskSessionID: "own-session", TaskID: "own-task", StartedAt: time.Now().UTC()}))
	h := NewHandlers(svc, nil, clarification.NewStore(0), nil, &ownQuestionCreator{repo: repo}, repo, repo, nil, nil, nil, nil, nil, testLogger(t))
	p := mcpscope.Principal{CallerTaskID: "own-task", CallerSessionID: "own-session", WorkspaceID: "ws-state-event", Surface: mcpprofile.SurfaceKanbanTask}
	ctx := mcpscope.WithPrincipal(context.Background(), p)
	q := []clarification.Question{{ID: "stable-own-q", Prompt: "Synthetic choice?", Options: []clarification.Option{{Label: "A", Description: "First"}, {Label: "B", Description: "Second"}}}}
	require.Empty(t, clarification.NormalizeAndValidateQuestions(q))
	return h, repo, ctx, q
}

func ownReceipt(t *testing.T, response *ws.Message) ownQuestionReceipt {
	t.Helper()
	require.Equal(t, ws.MessageTypeResponse, response.Type)
	var r ownQuestionReceipt
	require.NoError(t, json.Unmarshal(response.Payload, &r))
	return r
}

func TestOwnQuestionReceiptDurableDedupAndReadback(t *testing.T) {
	h, repo, ctx, q := ownQuestionFixture(t)
	statusMsg := makeWSMessage(t, ws.ActionMCPGetOwnQuestionStatus, map[string]any{"task_id": "own-task", "session_id": "own-session", "question_ids": []string{q[0].ID}})
	response, err := h.handleGetOwnQuestionStatus(ctx, statusMsg)
	require.NoError(t, err)
	absent := ownReceipt(t, response)
	require.Equal(t, "not_created", absent.Status)
	require.True(t, absent.RetryAllowed)
	ask := makeWSMessage(t, ws.ActionMCPAskUserQuestion, map[string]any{"task_id": "own-task", "session_id": "own-session", "questions": q, "context": "synthetic context", "return_mode": "receipt"})
	response, err = h.handleAskUserQuestion(ctx, ask)
	require.NoError(t, err)
	first := ownReceipt(t, response)
	require.Equal(t, "pending", first.Status)
	require.False(t, first.RetryAllowed)
	_, live := h.clarificationSvc.(*clarification.Store).GetRequest(first.PendingID)
	require.False(t, live, "receipt must use durable answer fallback, not an unwaited live store entry")
	// A backend restart loses all in-memory entries, while durable IDs survive.
	h.clarificationSvc = clarification.NewStore(0)
	response, err = h.handleAskUserQuestion(ctx, ask)
	require.NoError(t, err)
	require.Equal(t, first.PendingID, ownReceipt(t, response).PendingID)
	messages, err := repo.FindOwnClarificationMessages(ctx, "own-task", "own-session", []string{q[0].ID})
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.Empty(t, messages[0].Content, "reader must not load conversation content")
	// Preserve the original identity even after its waiter disconnected.
	messages[0].Metadata["agent_disconnected"] = true
	require.NoError(t, repo.UpdateMessage(ctx, messages[0]))
	response, err = h.handleGetOwnQuestionStatus(ctx, statusMsg)
	require.NoError(t, err)
	require.True(t, ownReceipt(t, response).Questions[0].AgentDisconnected)
	for _, terminal := range []string{"answered", "rejected", "expired", "cancelled"} {
		messages[0].Metadata["status"] = terminal
		if terminal == "answered" {
			messages[0].Metadata["response"] = &clarification.Answer{QuestionID: q[0].ID, SelectedOptions: []string{q[0].Options[0].ID}, CustomText: "synthetic recorded answer"}
		}
		require.NoError(t, repo.UpdateMessage(ctx, messages[0]))
		response, err = h.handleGetOwnQuestionStatus(ctx, statusMsg)
		require.NoError(t, err)
		r := ownReceipt(t, response)
		require.Equal(t, terminal, r.Status)
		require.False(t, r.RetryAllowed)
		if terminal == "answered" {
			require.Equal(t, "synthetic recorded answer", r.Questions[0].Answer.CustomText)
		}
		response, err = h.handleAskUserQuestion(ctx, ask)
		require.NoError(t, err)
		require.Equal(t, first.PendingID, ownReceipt(t, response).PendingID)
	}
}

func TestOwnQuestionRejectsCallerMismatchAndStableIDChanges(t *testing.T) {
	h, repo, ctx, q := ownQuestionFixture(t)
	ask := makeWSMessage(t, ws.ActionMCPAskUserQuestion, map[string]any{"session_id": "own-session", "questions": q, "return_mode": "receipt"})
	response, err := h.handleAskUserQuestion(ctx, ask)
	require.NoError(t, err)
	require.Equal(t, ws.MessageTypeResponse, response.Type)
	q[0].Prompt = "Changed synthetic question"
	response, err = h.handleAskUserQuestion(ctx, makeWSMessage(t, ws.ActionMCPAskUserQuestion, map[string]any{"session_id": "own-session", "questions": q, "return_mode": "receipt"}))
	require.NoError(t, err)
	require.Equal(t, ws.MessageTypeError, response.Type)
	for _, tc := range []struct {
		ctx               context.Context
		taskID, sessionID string
	}{
		{context.Background(), "own-task", "own-session"}, {ctx, "peer-task", "own-session"}, {ctx, "own-task", "peer-session"},
		{mcpscope.WithPrincipal(ctx, mcpscope.Principal{CallerTaskID: "own-task", CallerSessionID: "own-session", WorkspaceID: "foreign"}), "own-task", "own-session"},
	} {
		response, err = h.handleGetOwnQuestionStatus(tc.ctx, makeWSMessage(t, ws.ActionMCPGetOwnQuestionStatus, map[string]any{"task_id": tc.taskID, "session_id": tc.sessionID, "question_ids": []string{q[0].ID}}))
		require.NoError(t, err)
		require.Equal(t, ws.MessageTypeError, response.Type)
	}
	require.NoError(t, repo.UpdateTaskSessionState(ctx, "own-session", models.TaskSessionStateCancelled, ""))
	response, err = h.handleGetOwnQuestionStatus(ctx, makeWSMessage(t, ws.ActionMCPGetOwnQuestionStatus, map[string]any{"session_id": "own-session", "question_ids": []string{q[0].ID}}))
	require.NoError(t, err)
	require.Equal(t, ws.MessageTypeError, response.Type)
}

func TestOwnQuestionConcurrentRetryAndPublicationFailure(t *testing.T) {
	h, repo, ctx, q := ownQuestionFixture(t)
	creator := h.messageCreator.(*ownQuestionCreator)
	creator.fail = true
	response, err := h.handleAskUserQuestion(ctx, makeWSMessage(t, ws.ActionMCPAskUserQuestion, map[string]any{"session_id": "own-session", "questions": q, "return_mode": "receipt"}))
	require.NoError(t, err)
	require.Equal(t, ws.MessageTypeError, response.Type)
	r, _, err := h.readOwnQuestion(ctx, "own-task", "own-session", []string{q[0].ID})
	require.NoError(t, err)
	require.Equal(t, "not_created", r.Status)
	creator.fail = false
	var wg sync.WaitGroup
	results := make(chan *ws.Message, 4)
	for range 4 {
		wg.Go(func() {
			response, err := h.handleAskUserQuestion(ctx, makeWSMessage(t, ws.ActionMCPAskUserQuestion, map[string]any{"session_id": "own-session", "questions": q, "return_mode": "receipt"}))
			if err != nil {
				t.Error(err)
			}
			results <- response
		})
	}
	wg.Wait()
	close(results)
	var pending string
	for response := range results {
		r := ownReceipt(t, response)
		if pending == "" {
			pending = r.PendingID
		}
		require.Equal(t, pending, r.PendingID)
	}
	messages, err := repo.FindOwnClarificationMessages(ctx, "own-task", "own-session", []string{q[0].ID})
	require.NoError(t, err)
	require.Len(t, messages, 1)
}

func TestOwnQuestionLegacyAutogeneratedIDsRemainInteractionLocal(t *testing.T) {
	h, repo, ctx, q := ownQuestionFixture(t)
	q[0].ID = "q1"
	pending, _, err := h.prepareQuestionPublication(ctx, "own-task", "own-session", q, "")
	require.NoError(t, err)
	next := append([]clarification.Question(nil), q...)
	next[0].Prompt = "Different later synthetic question"
	later, _, err := h.prepareQuestionPublication(ctx, "own-task", "own-session", next, "")
	require.NoError(t, err)
	require.NotEqual(t, pending, later)
	messages, err := repo.FindOwnClarificationMessages(ctx, "own-task", "own-session", []string{"q1"})
	require.NoError(t, err)
	require.Len(t, messages, 2)
	_, _, err = h.readOwnQuestion(ctx, "own-task", "own-session", []string{"q1"})
	require.Error(t, err, "ambiguous legacy IDs must not guess an answer or permit recreation")
}
