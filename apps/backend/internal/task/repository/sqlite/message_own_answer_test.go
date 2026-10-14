package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/kandev/kandev/internal/orchestrator/messagequeue"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/ownanswer"
	"github.com/kandev/kandev/internal/testutil"
	"github.com/stretchr/testify/require"
	"strings"
	"sync"
	"testing"
	"time"
)

func ownAnswerEvidence(t *testing.T, r *Repository) (ownanswer.OwnAnswerBinding, *models.Message, *models.Message) {
	t.Helper()
	ctx := context.Background()
	seedForMsgTest(t, r, "answer-task", "answer-session", "question-turn")
	require.NoError(t, r.CreateWorkspace(ctx, &models.Workspace{ID: "answer-workspace", Name: "Synthetic"}))
	_, err := r.db.ExecContext(ctx, r.db.Rebind(`UPDATE tasks SET workspace_id=? WHERE id=?`), "answer-workspace", "answer-task")
	require.NoError(t, err)
	now := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Minute)
	q := ownanswer.Question{ID: "stable-q", Title: "Choice", Prompt: "Synthetic choice?", Options: []ownanswer.Option{{ID: "opt-a", Label: "A"}, {ID: "opt-b", Label: "B"}}}
	qm := &models.Message{ID: uuid.NewString(), TaskID: "answer-task", TaskSessionID: "answer-session", TurnID: "question-turn", Type: models.MessageTypeClarificationRequest, AuthorType: models.MessageAuthorAgent, Content: "synthetic question", RequestsInput: true, CreatedAt: now, UpdatedAt: now, Metadata: map[string]any{"question_id": q.ID, "pending_id": "pending-q", "question": q, "context": "Synthetic context", "status": "pending", "agent_disconnected": true}}
	require.NoError(t, r.CreateMessage(ctx, qm))
	require.NoError(t, r.CreateTurn(ctx, &models.Turn{ID: "later-human-turn", TaskID: qm.TaskID, TaskSessionID: qm.TaskSessionID, StartedAt: now.Add(time.Second)}))
	am := &models.Message{ID: uuid.NewString(), TaskID: qm.TaskID, TaskSessionID: qm.TaskSessionID, TurnID: "later-human-turn", Type: models.MessageTypeMessage, AuthorType: models.MessageAuthorUser, Content: "Context: Synthetic context\n\nQuestion 1: Choice\nSynthetic choice?\nAnswer: A", CreatedAt: now.Add(2 * time.Second), UpdatedAt: now.Add(2 * time.Second), Metadata: map[string]any{"client_message_request_fingerprint": strings.Repeat("a", 64), "queue_admission_ids": []string{uuid.NewString()}}}
	require.NoError(t, r.CreateMessage(ctx, am))
	recorded, err := r.GetMessage(ctx, qm.ID)
	require.NoError(t, err)
	raw, err := json.Marshal(recorded.Metadata["question"])
	require.NoError(t, err)
	return ownanswer.OwnAnswerBinding{TaskID: qm.TaskID, SessionID: qm.TaskSessionID, WorkspaceID: "answer-workspace", QuestionID: q.ID, PendingID: "pending-q", QuestionMessageID: qm.ID, QuestionSHA256: ownanswer.OwnAnswerDigest(raw), AnswerMessageID: am.ID, AnswerSHA256: ownanswer.OwnAnswerDigest([]byte(am.Content))}, qm, am
}

func ownAnswerAdmission(t *testing.T, r *Repository, am *models.Message) {
	t.Helper()
	ctx := context.Background()
	_, err := messagequeue.NewSQLiteRepository(r.db, r.db)
	require.NoError(t, err)
	s, err := r.GetTaskSession(ctx, am.TaskSessionID)
	require.NoError(t, err)
	ids := am.Metadata["queue_admission_ids"].([]string)
	queued := messagequeue.QueuedMessage{ID: ids[0], TaskID: am.TaskID, SessionID: am.TaskSessionID, Content: am.Content, QueuedBy: messagequeue.QueuedByUser, Metadata: map[string]any{"queue_admission_ids": ids}, QueuedAt: am.CreatedAt.Add(-time.Second)}
	raw, err := json.Marshal(queued)
	require.NoError(t, err)
	_, err = r.db.ExecContext(ctx, r.db.Rebind(`INSERT INTO queue_admission_receipts(task_id,session_id,session_incarnation_id,client_queue_id,request_fingerprint,accepted_queue_id,response_json,admitted_at) VALUES (?,?,?,?,?,?,?,?)`), am.TaskID, am.TaskSessionID, s.QueueIncarnationID, ids[0], strings.Repeat("b", 64), ids[0], string(raw), queued.QueuedAt)
	require.NoError(t, err)
}

func TestOwnAnswerRequiresDurableNativeAdmission(t *testing.T) {
	r := newRepoForSessionTests(t)
	b, _, _ := ownAnswerEvidence(t, r)
	_, _, err := r.ReconcileOwnQuestionAnswer(context.Background(), b)
	require.Error(t, err, "native-looking metadata alone is not Human provenance")
}

func TestOwnAnswerAtomicReconciliation(t *testing.T) {
	verifyOwnAnswerAtomic(t, newRepoForSessionTests(t))
}
func TestOwnAnswerPostgresAtomicReconciliation(t *testing.T) {
	pool := testutil.OpenIsolatedPostgres(t, testutil.PostgresDSNFromEnv(t))
	r, err := NewWithDB(pool, pool, nil)
	require.NoError(t, err)
	verifyOwnAnswerAtomic(t, r)
}
func verifyOwnAnswerAtomic(t *testing.T, r *Repository) {
	t.Helper()
	ctx := context.Background()
	b, qm, am := ownAnswerEvidence(t, r)
	ownAnswerAdmission(t, r, am)
	beforeSession, err := r.GetTaskSession(ctx, b.SessionID)
	require.NoError(t, err)
	beforeTask, err := r.GetTask(ctx, b.TaskID)
	require.NoError(t, err)
	var wg sync.WaitGroup
	changes := make(chan bool, 4)
	errors := make(chan error, 4)
	for range 4 {
		wg.Go(func() { _, changed, err := r.ReconcileOwnQuestionAnswer(ctx, b); changes <- changed; errors <- err })
	}
	wg.Wait()
	close(changes)
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	n := 0
	for c := range changes {
		if c {
			n++
		}
	}
	require.Equal(t, 1, n)
	got, err := r.GetMessage(ctx, qm.ID)
	require.NoError(t, err)
	require.Equal(t, "answered", got.Metadata["status"])
	require.NotNil(t, got.Metadata[ownanswer.OwnAnswerReceiptKey])
	require.Equal(t, qm.Content, got.Content)
	human, err := r.GetMessage(ctx, am.ID)
	require.NoError(t, err)
	require.Equal(t, am.Content, human.Content)
	require.NotEmpty(t, human.Metadata[ownanswer.OwnAnswerBindingKey])
	afterSession, err := r.GetTaskSession(ctx, b.SessionID)
	require.NoError(t, err)
	require.Equal(t, beforeSession, afterSession)
	afterTask, err := r.GetTask(ctx, b.TaskID)
	require.NoError(t, err)
	require.Equal(t, beforeTask, afterTask)
	row, err := r.FindOwnClarificationMessages(ctx, b.TaskID, b.SessionID, []string{b.QuestionID})
	require.NoError(t, err)
	require.Len(t, row, 1)
	require.Equal(t, "answered", row[0].Metadata["status"])
}

func TestOwnAnswerEvidenceDenials(t *testing.T) {
	cases := map[string]func(*Repository, *ownanswer.OwnAnswerBinding, *models.Message, *models.Message){
		"changed-hash": func(_ *Repository, b *ownanswer.OwnAnswerBinding, _, _ *models.Message) {
			b.AnswerSHA256 = strings.Repeat("0", 64)
		},
		"foreign-scope": func(_ *Repository, b *ownanswer.OwnAnswerBinding, _, _ *models.Message) { b.WorkspaceID = "foreign" },
		"agent-proxy": func(_ *Repository, _ *ownanswer.OwnAnswerBinding, _, a *models.Message) {
			a.Metadata["sender_task_id"] = "peer"
		},
		"unknown-provenance": func(_ *Repository, _ *ownanswer.OwnAnswerBinding, _, a *models.Message) {
			a.Metadata["unknown_origin"] = true
		},
		"edited-Human": func(_ *Repository, _ *ownanswer.OwnAnswerBinding, _, a *models.Message) {
			a.UpdatedAt = a.UpdatedAt.Add(time.Second)
		},
		"fabricated-choice": func(_ *Repository, b *ownanswer.OwnAnswerBinding, _, a *models.Message) {
			a.Content += " extra instructions"
			b.AnswerSHA256 = ownanswer.OwnAnswerDigest([]byte(a.Content))
		},
		"still-attached": func(_ *Repository, _ *ownanswer.OwnAnswerBinding, q, _ *models.Message) {
			q.Metadata["agent_disconnected"] = false
		},
		"answered-elsewhere": func(_ *Repository, _ *ownanswer.OwnAnswerBinding, q, _ *models.Message) {
			q.Metadata["status"] = "answered"
		},
		"terminal-session": func(r *Repository, _ *ownanswer.OwnAnswerBinding, _, _ *models.Message) {
			require.NoError(t, r.UpdateTaskSessionState(context.Background(), "answer-session", models.TaskSessionStateCancelled, ""))
		},
		"ambiguous-question": func(r *Repository, _ *ownanswer.OwnAnswerBinding, q, _ *models.Message) {
			copy := *q
			copy.ID = uuid.NewString()
			copy.Metadata = map[string]any{}
			for k, v := range q.Metadata {
				copy.Metadata[k] = v
			}
			copy.Metadata["pending_id"] = "other-pending"
			require.NoError(t, r.CreateMessage(context.Background(), &copy))
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRepoForSessionTests(t)
			b, q, a := ownAnswerEvidence(t, r)
			ownAnswerAdmission(t, r, a)
			mutate(r, &b, q, a)
			for _, m := range []*models.Message{q, a} {
				raw, e := json.Marshal(m.Metadata)
				require.NoError(t, e)
				_, e = r.db.ExecContext(context.Background(), r.db.Rebind(`UPDATE task_session_messages SET metadata=?,content=?,updated_at=? WHERE id=?`), string(raw), m.Content, m.UpdatedAt, m.ID)
				require.NoError(t, e)
			}
			_, _, err := r.ReconcileOwnQuestionAnswer(context.Background(), b)
			require.Error(t, err, fmt.Sprintf("must reject %s", name))
			got, e := r.GetMessage(context.Background(), q.ID)
			require.NoError(t, e)
			require.Nil(t, got.Metadata[ownanswer.OwnAnswerReceiptKey])
		})
	}
}

func TestOwnAnswerReopensNormalMaterialAdmissionOnly(t *testing.T) {
	r := newRepoForSessionTests(t)
	ctx := context.Background()
	b, _, am := ownAnswerEvidence(t, r)
	ownAnswerAdmission(t, r, am)
	s, err := r.GetTaskSession(ctx, b.SessionID)
	require.NoError(t, err)
	makePair := func() (*models.Message, *messagequeue.QueuedMessage) {
		id := uuid.NewString()
		now := time.Now().UTC()
		m := &models.Message{ID: id, TaskID: b.TaskID, TaskSessionID: b.SessionID, TurnID: "later-human-turn", AuthorType: models.MessageAuthorUser, Type: models.MessageTypeMessage, Content: "Synthetic normal child report", CreatedAt: now, UpdatedAt: now, Metadata: map[string]any{"sender_task_id": "synthetic-child", "sender_session_id": "synthetic-child-session", "task_message_target_step": "", "task_message_target_incarnation": s.QueueIncarnationID}}
		q := &messagequeue.QueuedMessage{ID: id, TaskID: b.TaskID, SessionID: b.SessionID, Content: m.Content, QueuedBy: messagequeue.QueuedByUser, Metadata: map[string]any{messagequeue.MetadataDurableTranscriptMessageID: id, "user_message_recorded": true}}
		return m, q
	}
	m, q := makePair()
	require.Error(t, r.CreateTaskMessageAndQueue(ctx, m, q, nil, s.State, 10))
	_, _, err = r.ReconcileOwnQuestionAnswer(ctx, b)
	require.NoError(t, err)
	m, q = makePair()
	require.NoError(t, r.CreateTaskMessageAndQueue(ctx, m, q, nil, s.State, 10))
	other := &models.Message{TaskID: b.TaskID, TaskSessionID: b.SessionID, TurnID: "later-human-turn", AuthorType: models.MessageAuthorAgent, Type: models.MessageTypeClarificationRequest, Content: "Unanswered independent question", RequestsInput: true, Metadata: map[string]any{"question_id": "other-q", "pending_id": "other-pending", "status": "pending"}}
	require.NoError(t, r.CreateMessage(ctx, other))
	_, changed, err := r.ReconcileOwnQuestionAnswer(ctx, b)
	require.NoError(t, err)
	require.False(t, changed)
	m, q = makePair()
	require.Error(t, r.CreateTaskMessageAndQueue(ctx, m, q, nil, s.State, 10))
	unchanged, err := r.GetMessage(ctx, other.ID)
	require.NoError(t, err)
	require.Equal(t, "pending", unchanged.Metadata["status"])
}
