package handlers

import (
	"testing"

	"github.com/kandev/kandev/internal/clarification"
	ws "github.com/kandev/kandev/pkg/websocket"
	"github.com/stretchr/testify/require"
)

func TestOwnQuestionBatchReadback(t *testing.T) {
	h, repo, ctx, q := ownQuestionFixture(t)
	read := func(ids ...string) ownQuestionReceipt {
		response, err := h.handleGetOwnQuestionStatus(ctx, makeWSMessage(t, ws.ActionMCPGetOwnQuestionStatus, map[string]any{"session_id": "own-session", "question_ids": ids}))
		require.NoError(t, err)
		return ownReceipt(t, response)
	}
	missing := read("missing-a", "missing-b")
	require.Len(t, missing.Questions, 2)
	require.True(t, missing.RetryAllowed)
	for _, item := range missing.Questions {
		require.Equal(t, "not_created", item.Status)
		require.True(t, item.RetryAllowed)
		require.Empty(t, item.MessageID)
	}
	pending, _, err := h.prepareOwnQuestion(ctx, "own-task", "own-session", q, "", false)
	require.NoError(t, err)
	original := read(q[0].ID)
	require.Equal(t, pending, original.Questions[0].PendingID)
	require.False(t, original.Questions[0].RetryAllowed)
	for _, ids := range [][]string{{q[0].ID, "missing-a"}, {"missing-a", q[0].ID}} {
		mixed := read(ids...)
		require.Equal(t, "mixed", mixed.Status)
		require.False(t, mixed.RetryAllowed)
		require.Equal(t, pending, mixed.PendingID)
		for i, id := range ids {
			require.Equal(t, id, mixed.Questions[i].QuestionID)
			if id == q[0].ID {
				require.Equal(t, original.Questions[0], mixed.Questions[i])
			} else {
				require.True(t, mixed.Questions[i].RetryAllowed)
			}
		}
		require.Equal(t, mixed, read(ids...), "readback must be idempotent")
	}
	messages, err := repo.FindOwnClarificationMessages(ctx, "own-task", "own-session", []string{q[0].ID})
	require.NoError(t, err)
	require.Len(t, messages, 1)
	m := messages[0]
	for _, status := range []string{"answered", "rejected"} {
		m.Metadata["status"] = status
		m.Metadata["response"] = clarification.Answer{QuestionID: q[0].ID, SelectedOptions: []string{"A"}}
		require.NoError(t, repo.UpdateMessage(ctx, m))
		result := read("missing-a", q[0].ID)
		require.Equal(t, "mixed", result.Status)
		require.Equal(t, status, result.Questions[1].Status)
		require.Equal(t, m.ID, result.Questions[1].MessageID)
		require.False(t, result.Questions[1].RetryAllowed)
		if status == "answered" {
			require.Equal(t, []string{"A"}, result.Questions[1].Answer.SelectedOptions)
		} else {
			require.Nil(t, result.Questions[1].Answer)
		}
	}
}

func TestOwnQuestionBatchRetryRejectsPartialAndSeparateRequests(t *testing.T) {
	h, repo, ctx, q := ownQuestionFixture(t)
	first, _, err := h.prepareOwnQuestion(ctx, "own-task", "own-session", q, "", false)
	require.NoError(t, err)
	second := q[0]
	second.ID = "second-own-q"
	combined := []clarification.Question{q[0], second}
	_, _, err = h.prepareOwnQuestion(ctx, "own-task", "own-session", combined, "", false)
	require.Error(t, err, "partial existing identity must not publish or return a partial retry")
	rows, err := repo.FindOwnClarificationMessages(ctx, "own-task", "own-session", []string{second.ID})
	require.NoError(t, err)
	require.Empty(t, rows)
	next, _, err := h.prepareOwnQuestion(ctx, "own-task", "own-session", []clarification.Question{second}, "", false)
	require.NoError(t, err)
	require.NotEqual(t, first, next)
	receipt, _, err := h.readOwnQuestion(ctx, "own-task", "own-session", []string{q[0].ID, second.ID})
	require.NoError(t, err, "reader permits separate own requests")
	require.Empty(t, receipt.PendingID, "no single bundle identity may be implied")
	require.Equal(t, first, receipt.Questions[0].PendingID)
	require.Equal(t, next, receipt.Questions[1].PendingID)
	receipt, _, err = h.readOwnQuestion(ctx, "own-task", "own-session", []string{"missing-a", q[0].ID, "missing-b", second.ID})
	require.NoError(t, err)
	require.Len(t, receipt.Questions, 4)
	require.Equal(t, "mixed", receipt.Status)
	require.True(t, receipt.Questions[0].RetryAllowed)
	require.False(t, receipt.Questions[1].RetryAllowed)
	require.True(t, receipt.Questions[2].RetryAllowed)
	require.False(t, receipt.Questions[3].RetryAllowed)
	_, _, err = h.prepareOwnQuestion(ctx, "own-task", "own-session", combined, "", false)
	require.Error(t, err, "ask retry cannot merge distinct requests")
	for _, question := range combined {
		_, original, err := h.prepareOwnQuestion(ctx, "own-task", "own-session", []clarification.Question{question}, "", false)
		require.NoError(t, err)
		require.Len(t, original.Questions, 1)
	}
}
