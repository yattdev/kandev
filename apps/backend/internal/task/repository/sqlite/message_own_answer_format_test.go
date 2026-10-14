package sqlite

import (
	"context"
	"encoding/json"
	"maps"
	"testing"

	"github.com/kandev/kandev/internal/task/ownanswer"
	"github.com/stretchr/testify/require"
)

func TestOwnAnswerUnsupportedFormatPreservesEvidence(t *testing.T) {
	r := newRepoForSessionTests(t)
	ctx := context.Background()
	b, qm, am := ownAnswerEvidence(t, r)
	// Exact output shape of formatAgentMessageCommentsAsMarkdown. The quoted
	// selection and multiline feedback are not a structured option selection.
	am.Content = "### Agent Message Comments\n\nSelected agent response:\n> Synthetic choice?\n\nUser feedback:\n> Monitor only these four.\n> Keep conclusions qualified.\n\n---\n"
	b.AnswerSHA256 = ownanswer.OwnAnswerDigest([]byte(am.Content))
	_, err := r.db.ExecContext(ctx, `UPDATE task_session_messages SET content=? WHERE id=?`, am.Content, am.ID)
	require.NoError(t, err)
	ownAnswerAdmission(t, r, am)
	other := *qm
	other.ID = "independent-question-message"
	other.Metadata = maps.Clone(qm.Metadata)
	question := qm.Metadata["question"].(ownanswer.Question)
	question.ID, question.Title, question.Prompt = "independent-question", "Independent", "A different question?"
	other.Metadata["question"], other.Metadata["question_id"], other.Metadata["pending_id"] = question, question.ID, "independent-pending"
	require.NoError(t, r.CreateMessage(ctx, &other))
	beforeOther, err := r.GetMessage(ctx, other.ID)
	require.NoError(t, err)
	beforeQ, err := r.GetMessage(ctx, qm.ID)
	require.NoError(t, err)
	beforeA, err := r.GetMessage(ctx, am.ID)
	require.NoError(t, err)
	beforeS, err := r.GetTaskSession(ctx, b.SessionID)
	require.NoError(t, err)
	beforeT, err := r.GetTask(ctx, b.TaskID)
	require.NoError(t, err)
	rows, changed, err := r.ReconcileOwnQuestionAnswer(ctx, b)
	require.EqualError(t, err, "ANSWER_FORMAT_UNSUPPORTED")
	require.Empty(t, rows)
	require.False(t, changed)
	afterQ, err := r.GetMessage(ctx, qm.ID)
	require.NoError(t, err)
	afterA, err := r.GetMessage(ctx, am.ID)
	require.NoError(t, err)
	afterS, err := r.GetTaskSession(ctx, b.SessionID)
	require.NoError(t, err)
	afterT, err := r.GetTask(ctx, b.TaskID)
	require.NoError(t, err)
	require.Equal(t, beforeQ, afterQ)
	require.Equal(t, beforeA, afterA)
	require.Equal(t, beforeS, afterS)
	require.Equal(t, beforeT, afterT)
	afterOther, err := r.GetMessage(ctx, other.ID)
	require.NoError(t, err)
	require.Equal(t, beforeOther, afterOther)
}

func TestOwnAnswerFormatDispositionRequiresProvenanceAndUnclaimedQuestion(t *testing.T) {
	for _, invalid := range []string{"missing-admission", "changed-hash", "foreign-workspace", "claimed-question", "ambiguous-question"} {
		t.Run(invalid, func(t *testing.T) {
			r := newRepoForSessionTests(t)
			ctx := context.Background()
			b, q, a := ownAnswerEvidence(t, r)
			a.Content += "\nFree-text qualifications"
			b.AnswerSHA256 = ownanswer.OwnAnswerDigest([]byte(a.Content))
			_, err := r.db.ExecContext(ctx, `UPDATE task_session_messages SET content=? WHERE id=?`, a.Content, a.ID)
			require.NoError(t, err)
			if invalid != "missing-admission" {
				ownAnswerAdmission(t, r, a)
			}
			switch invalid {
			case "changed-hash":
				b.AnswerSHA256 = b.QuestionSHA256
			case "foreign-workspace":
				b.WorkspaceID = "foreign"
			case "claimed-question":
				q.Metadata["status"] = "answered"
				raw, e := json.Marshal(q.Metadata)
				require.NoError(t, e)
				_, e = r.db.ExecContext(ctx, `UPDATE task_session_messages SET metadata=? WHERE id=?`, string(raw), q.ID)
				require.NoError(t, e)
			case "ambiguous-question":
				q.ID = "another-question"
				q.Metadata["pending_id"] = "another-pending"
				require.NoError(t, r.CreateMessage(ctx, q))
			}
			_, changed, err := r.ReconcileOwnQuestionAnswer(ctx, b)
			require.Error(t, err)
			require.NotEqual(t, "ANSWER_FORMAT_UNSUPPORTED", err.Error())
			require.False(t, changed)
		})
	}
}
