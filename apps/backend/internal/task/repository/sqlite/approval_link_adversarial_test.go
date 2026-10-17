package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/ownanswer"
	"github.com/stretchr/testify/require"
)

func approvalFixture(t *testing.T) (*Repository, context.Context, ownanswer.OwnAnswerBinding, *models.Message, *models.Message) {
	t.Helper()
	return approvalFixtureOnRepo(t, newRepoForSessionTests(t))
}

func approvalFixtureOnRepo(t *testing.T, r *Repository) (*Repository, context.Context, ownanswer.OwnAnswerBinding, *models.Message, *models.Message) {
	t.Helper()
	b, q, a := ownAnswerEvidence(t, r)
	a.Content = syntheticApprovalComments
	b.AnswerSHA256 = ownanswer.OwnAnswerDigest([]byte(a.Content))
	_, err := r.db.Exec(`UPDATE task_session_messages SET content=? WHERE id=?`, a.Content, a.ID)
	require.NoError(t, err)
	ownAnswerAdmission(t, r, a)
	ctx := seedApprovalBrowserAuthority(t, r)
	return r, ctx, b, q, a
}

func TestApprovalLinkConcurrentConflictingOperationsHaveOneWinner(t *testing.T) {
	r, ctx, b, _, _ := approvalFixture(t)
	g, err := r.createApprovalLinkGrant(ctx, ownanswer.ApprovalLinkRequest{OwnAnswerBinding: b, OptionID: "opt-a"})
	require.NoError(t, err)
	var wg sync.WaitGroup
	results := make(chan error, 4)
	for i := range 4 {
		wg.Go(func() {
			_, changed, err := r.consumeApprovalLinkGrant(ctx, b, g.ID, fmt.Sprintf("synthetic-operation-%d", i), g.Incarnation)
			if err == nil && !changed {
				err = fmt.Errorf("conflicting operation returned an idempotent success")
			}
			results <- err
		})
	}
	wg.Wait()
	close(results)
	winners, losers := 0, 0
	for err := range results {
		if err == nil {
			winners++
		} else {
			losers++
		}
	}
	require.Equal(t, 1, winners)
	require.Equal(t, 3, losers)
}

func saveApprovalMessage(t *testing.T, r *Repository, m *models.Message) {
	t.Helper()
	raw, err := json.Marshal(m.Metadata)
	require.NoError(t, err)
	_, err = r.db.Exec(`UPDATE task_session_messages SET metadata=?,content=?,updated_at=? WHERE id=?`, string(raw), m.Content, m.UpdatedAt, m.ID)
	require.NoError(t, err)
}

func TestApprovalLinkCreatorRejectsEditedOrProxyEvidence(t *testing.T) {
	for _, invalid := range []string{"edited-answer", "proxy", "unknown-metadata", "bad-fingerprint", "no-fingerprint", "invalid-option", "duplicate-option", "missing-receipt", "null-response", "null-binding"} {
		t.Run(invalid, func(t *testing.T) {
			r, ctx, b, q, a := approvalFixture(t)
			option := "opt-a"
			switch invalid {
			case "edited-answer":
				a.UpdatedAt = a.UpdatedAt.Add(time.Second)
			case "proxy":
				a.Metadata["sender_task_id"] = "peer"
			case "unknown-metadata":
				a.Metadata["unknown"] = true
			case "bad-fingerprint":
				a.Metadata["client_message_request_fingerprint"] = strings.Repeat("z", 64)
			case "no-fingerprint":
				delete(a.Metadata, "client_message_request_fingerprint")
			case "null-response":
				q.Metadata["response"] = nil
			case "null-binding":
				a.Metadata[ownanswer.OwnAnswerBindingKey] = nil
			case "invalid-option":
				option = "invented"
			case "duplicate-option":
				question := q.Metadata["question"].(ownanswer.Question)
				question.Options[1].ID = question.Options[0].ID
				q.Metadata["question"] = question
				raw, err := json.Marshal(question)
				require.NoError(t, err)
				b.QuestionSHA256 = ownanswer.OwnAnswerDigest(raw)
			case "missing-receipt":
				_, err := r.db.Exec(`DELETE FROM queue_admission_receipts`)
				require.NoError(t, err)
			}
			saveApprovalMessage(t, r, q)
			saveApprovalMessage(t, r, a)
			before := snapshotOwnAnswerBoundary(t, r)
			_, err := r.createApprovalLinkGrant(ctx, ownanswer.ApprovalLinkRequest{OwnAnswerBinding: b, OptionID: option})
			require.Error(t, err)
			require.Equal(t, before, snapshotOwnAnswerBoundary(t, r))
			var count int
			require.NoError(t, r.db.Get(&count, `SELECT count(*) FROM own_answer_approval_link_grants`))
			require.Zero(t, count)
		})
	}
}

func TestApprovalLinkConsumptionDenialsPreserveRecords(t *testing.T) {
	for _, invalid := range []string{"task", "session", "workspace", "question", "pending", "question-message", "answer-message", "question-hash", "answer-hash", "incarnation", "missing-grant", "expired", "revoked", "context", "question-body", "option", "answer-body", "answer-metadata", "receipt", "receipt-fingerprint", "ambiguous", "short-operation", "blank-operation"} {
		t.Run(invalid, func(t *testing.T) {
			r, ctx, b, q, a := approvalFixture(t)
			g, err := r.createApprovalLinkGrant(ctx, ownanswer.ApprovalLinkRequest{OwnAnswerBinding: b, OptionID: "opt-a"})
			require.NoError(t, err)
			incarnation, id, operation := g.Incarnation, g.ID, "synthetic-operation"
			switch invalid {
			case "task":
				b.TaskID = "foreign"
			case "session":
				b.SessionID = "foreign"
			case "workspace":
				b.WorkspaceID = "foreign"
			case "question":
				b.QuestionID = "foreign"
			case "pending":
				b.PendingID = "foreign"
			case "question-message":
				b.QuestionMessageID = "foreign"
			case "answer-message":
				b.AnswerMessageID = "foreign"
			case "question-hash":
				b.QuestionSHA256 = strings.Repeat("0", 64)
			case "answer-hash":
				b.AnswerSHA256 = strings.Repeat("0", 64)
			case "incarnation":
				incarnation = "foreign"
			case "missing-grant":
				id = "missing"
			case "expired":
				r.clockNow = func() time.Time { return g.ExpiresAt }
			case "revoked":
				_, err = r.db.Exec(`UPDATE own_answer_approval_link_grants SET state='revoked'`)
				require.NoError(t, err)
			case "context":
				q.Metadata["context"] = "altered context"
				saveApprovalMessage(t, r, q)
			case "question-body":
				q.Content += " changed"
				saveApprovalMessage(t, r, q)
			case "option":
				question := q.Metadata["question"].(ownanswer.Question)
				question.Options[0].Description = "altered"
				q.Metadata["question"] = question
				saveApprovalMessage(t, r, q)
			case "answer-body":
				a.Content += " changed"
				saveApprovalMessage(t, r, a)
			case "answer-metadata":
				a.Metadata["client_message_request_fingerprint"] = strings.Repeat("c", 64)
				saveApprovalMessage(t, r, a)
			case "receipt":
				_, err = r.db.Exec(`UPDATE queue_admission_receipts SET response_json='{}'`)
				require.NoError(t, err)
			case "receipt-fingerprint":
				_, err = r.db.Exec(`UPDATE queue_admission_receipts SET request_fingerprint='changed'`)
				require.NoError(t, err)
			case "ambiguous":
				q.ID = "ambiguous"
				q.Metadata["pending_id"] = "another"
				require.NoError(t, r.CreateMessage(ctx, q))
			case "short-operation":
				operation = "x"
			case "blank-operation":
				operation = strings.Repeat(" ", 12)
			}
			before := snapshotOwnAnswerBoundary(t, r)
			_, changed, err := r.consumeApprovalLinkGrant(ctx, b, id, operation, incarnation)
			require.Error(t, err)
			require.False(t, changed)
			require.Equal(t, before, snapshotOwnAnswerBoundary(t, r))
			var state string
			require.NoError(t, r.db.Get(&state, `SELECT state FROM own_answer_approval_link_grants WHERE id=?`, g.ID))
			require.NotEqual(t, "consumed", state)
		})
	}
}

func TestApprovalLinkRollbackAndConcurrentWinner(t *testing.T) {
	r, ctx, b, _, a := approvalFixture(t)
	g, err := r.createApprovalLinkGrant(ctx, ownanswer.ApprovalLinkRequest{OwnAnswerBinding: b, OptionID: "opt-a"})
	require.NoError(t, err)
	before := snapshotOwnAnswerBoundary(t, r)
	_, err = r.db.Exec(`CREATE TRIGGER fail_approval_answer BEFORE UPDATE ON task_session_messages WHEN OLD.id='` + a.ID + `' BEGIN SELECT RAISE(ABORT, 'synthetic answer persistence failure'); END`)
	require.NoError(t, err)
	_, _, err = r.consumeApprovalLinkGrant(ctx, b, g.ID, "synthetic-operation", g.Incarnation)
	require.ErrorContains(t, err, "synthetic answer persistence failure")
	require.Equal(t, before, snapshotOwnAnswerBoundary(t, r))
	var state string
	require.NoError(t, r.db.Get(&state, `SELECT state FROM own_answer_approval_link_grants WHERE id=?`, g.ID))
	require.Equal(t, "issued", state)
	_, err = r.db.Exec(`DROP TRIGGER fail_approval_answer`)
	require.NoError(t, err)
	var wg sync.WaitGroup
	results := make(chan bool, 4)
	errors := make(chan error, 4)
	for range 4 {
		wg.Go(func() {
			_, changed, err := r.consumeApprovalLinkGrant(ctx, b, g.ID, "synthetic-operation", g.Incarnation)
			results <- changed
			errors <- err
		})
	}
	wg.Wait()
	close(results)
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	winners := 0
	for changed := range results {
		if changed {
			winners++
		}
	}
	require.Equal(t, 1, winners)
	_, err = r.createApprovalLinkGrant(ctx, ownanswer.ApprovalLinkRequest{OwnAnswerBinding: b, OptionID: "opt-a"})
	require.Error(t, err)
}
