package sqlite

import (
	"context"
	"encoding/json"
	"maps"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/orchestrator/messagequeue"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/ownanswer"
	"github.com/stretchr/testify/require"
)

// Contract coverage requested by Support cb42dde1. These tests document the
// existing refusal boundary; no new authority or production behavior is added.
// The two-comment shape comes from formatAgentMessageCommentsAsMarkdown, which
// serializes text but does not retain question, option or selected-message IDs.
const syntheticApprovalComments = "### Agent Message Comments\n\n" +
	"Selected agent response:\n> Other work remains.\n\nUser feedback:\n> I don't understand\n\n" +
	"Selected agent response:\n> Recommended: allow the existing validator's exact checks.\n\n" +
	"User feedback:\n> ok go\n\n---"

func TestOwnAnswerNativeCommentsRequireIndependentBindingEvidence(t *testing.T) {
	for _, tc := range []struct {
		name       string
		comments   bool
		admission  bool
		wantReason string
	}{
		{"direct-native-comments", true, false, "native Human ingress provenance unavailable"},
		{"direct-native-exact-choice", false, false, "native Human ingress provenance unavailable"},
		{"queued-native-comments", true, true, "ANSWER_FORMAT_UNSUPPORTED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRepoForSessionTests(t)
			ctx := context.Background()
			b, qm, am := ownAnswerEvidence(t, r)
			if tc.comments {
				am.Content = syntheticApprovalComments
			}
			if tc.admission {
				ownAnswerAdmission(t, r, am)
			} else {
				delete(am.Metadata, "queue_admission_ids")
			}
			raw, err := json.Marshal(am.Metadata)
			require.NoError(t, err)
			_, err = r.db.ExecContext(ctx, `UPDATE task_session_messages SET content=?,metadata=? WHERE id=?`, am.Content, string(raw), am.ID)
			require.NoError(t, err)
			b.AnswerSHA256 = ownanswer.OwnAnswerDigest([]byte(am.Content))
			seedOwnAnswerIndependentEvidence(t, r, qm, am)
			before := snapshotOwnAnswerBoundary(t, r)
			rows, changed, err := r.ReconcileOwnQuestionAnswer(ctx, b)
			require.EqualError(t, err, tc.wantReason)
			require.Empty(t, rows)
			require.False(t, changed)
			require.Equal(t, before, snapshotOwnAnswerBoundary(t, r))
		})
	}
}

func seedOwnAnswerIndependentEvidence(t *testing.T, r *Repository, qm, am *models.Message) {
	t.Helper()
	ctx := context.Background()
	other := *qm
	other.ID = "independent-question-message"
	other.Metadata = maps.Clone(qm.Metadata)
	q := qm.Metadata["question"].(ownanswer.Question)
	q.ID, q.Title, q.Prompt = "independent-question", "Independent", "An independent question?"
	other.Metadata["question"], other.Metadata["question_id"], other.Metadata["pending_id"] = q, q.ID, "independent-pending"
	require.NoError(t, r.CreateMessage(ctx, &other))
	fact := *am
	fact.ID, fact.Content, fact.Metadata = "independent-human-fact", "my setup: Ubuntu Linux", maps.Clone(am.Metadata)
	fact.CreatedAt = am.CreatedAt.Add(time.Second)
	fact.UpdatedAt = fact.CreatedAt
	require.NoError(t, r.CreateMessage(ctx, &fact))
	_, err := messagequeue.NewSQLiteRepository(r.db, r.db)
	require.NoError(t, err)
	for i, content := range []string{"first retained FIFO entry", "second retained FIFO entry"} {
		_, err = r.db.ExecContext(ctx, `INSERT INTO queued_messages(id,session_id,task_id,position,content,queued_at,queued_by) VALUES (?,?,?,?,?,?,?)`, content, qm.TaskSessionID, qm.TaskID, i, content, time.Now().UTC(), "user")
		require.NoError(t, err)
	}
}

func snapshotOwnAnswerBoundary(t *testing.T, r *Repository) map[string][]map[string]any {
	t.Helper()
	snapshot := map[string][]map[string]any{}
	for _, table := range []string{"task_session_messages", "tasks", "task_sessions", "queued_messages", "queue_admission_receipts"} {
		// Fixed test-owned identifiers only; includes FIFO order, raw metadata,
		// all timestamps, admission receipts, question and separate Human fact.
		rows, err := r.db.Queryx("SELECT * FROM " + table + " ORDER BY 1")
		require.NoError(t, err)
		for rows.Next() {
			row := map[string]any{}
			require.NoError(t, rows.MapScan(row))
			for k, v := range row {
				if bytes, ok := v.([]byte); ok {
					row[k] = string(bytes)
				}
			}
			snapshot[table] = append(snapshot[table], row)
		}
		require.NoError(t, rows.Err())
		require.NoError(t, rows.Close())
	}
	return snapshot
}
