package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jmoiron/sqlx"
	"github.com/kandev/kandev/internal/orchestrator/messagequeue"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/ownanswer"
	"github.com/kandev/kandev/internal/task/repository/plancommenttx"
	"reflect"
	"time"
)

// ReconcileOwnQuestionAnswer commits only evidence linkage and question status.
// Task -> session locks serialize against input admission and terminal writers.
func (r *Repository) ReconcileOwnQuestionAnswer(ctx context.Context, b ownanswer.OwnAnswerBinding) ([]*models.Message, bool, error) {
	if err := b.Validate(); err != nil {
		return nil, false, err
	}
	tx, release, err := r.beginPlanCommentTx(ctx, b.TaskID)
	if err != nil {
		return nil, false, err
	}
	defer release()
	defer func() { _ = tx.Rollback() }()
	if err = r.guardOwnAnswerScope(ctx, tx, b); err != nil {
		return nil, false, err
	}
	qm, err := r.loadOwnAnswerMessage(ctx, tx, b, b.QuestionMessageID)
	if err != nil {
		return nil, false, err
	}
	am, err := r.loadOwnAnswerMessage(ctx, tx, b, b.AnswerMessageID)
	if err != nil {
		return nil, false, err
	}
	answer, receipt, err := ownanswer.CorrelateOwnAnswer(b, qm, am, r.nowUTC())
	if errors.Is(err, ownanswer.ErrAnswerFormatUnsupported) {
		return nil, false, r.unsupportedOwnAnswerDisposition(ctx, tx, b, qm, am)
	}
	if err != nil {
		return nil, false, err
	}
	if err = r.verifyOwnHumanAdmission(ctx, tx, b, am, receipt); err != nil {
		return nil, false, err
	}
	if prior := qm.Metadata[ownanswer.OwnAnswerReceiptKey]; prior != nil {
		if !ownAnswerReplayMatches(prior, am, receipt, answer, qm) {
			return nil, false, errors.New("answer binding conflicts")
		}
		return []*models.Message{qm}, false, tx.Commit()
	}
	if err := guardOwnAnswerUnclaimed(qm, am); err != nil {
		return nil, false, err
	}
	if err = r.guardOwnAnswerAmbiguity(ctx, tx, b, qm, am); err != nil {
		return nil, false, err
	}
	if err = r.persistOwnAnswerBinding(ctx, tx, b, qm, am, answer, receipt); err != nil {
		return nil, false, err
	}
	return []*models.Message{qm}, true, tx.Commit()
}

// Format alone proves no Human authority. Before reporting a non-retryable
// disposition, require all the normal read-only guards. Rollback leaves both
// messages and every queue/session/task record untouched.
func (r *Repository) unsupportedOwnAnswerDisposition(ctx context.Context, tx *sqlx.Tx, b ownanswer.OwnAnswerBinding, qm, am *models.Message) error {
	if err := r.verifyOwnHumanAdmission(ctx, tx, b, am, &ownanswer.OwnAnswerReceipt{}); err != nil {
		return err
	}
	if err := guardOwnAnswerUnclaimed(qm, am); err != nil {
		return err
	}
	if err := r.guardOwnAnswerAmbiguity(ctx, tx, b, qm, am); err != nil {
		return err
	}
	return ownanswer.ErrAnswerFormatUnsupported
}

func ownAnswerReplayMatches(prior any, am *models.Message, want *ownanswer.OwnAnswerReceipt, answer *ownanswer.Answer, qm *models.Message) bool {
	raw, err := json.Marshal(prior)
	var got ownanswer.OwnAnswerReceipt
	if err != nil || json.Unmarshal(raw, &got) != nil {
		return false
	}
	got.WorkspaceID = want.WorkspaceID
	want.BoundAt = got.BoundAt
	raw, err = json.Marshal(qm.Metadata["response"])
	var stored ownanswer.Answer
	return err == nil && json.Unmarshal(raw, &stored) == nil && reflect.DeepEqual(got, *want) && reflect.DeepEqual(stored, *answer) && qm.Metadata["status"] == clarificationStatusAnswered && am.Metadata[ownanswer.OwnAnswerBindingKey] == got.ID
}

func (r *Repository) loadOwnAnswerMessage(ctx context.Context, tx *sqlx.Tx, b ownanswer.OwnAnswerBinding, id string) (*models.Message, error) {
	rows, err := tx.QueryxContext(ctx, r.db.Rebind(`SELECT id,task_session_id,task_id,turn_id,author_type,author_id,content,requests_input,type,metadata,created_at,updated_at FROM task_session_messages WHERE id=? AND task_id=? AND task_session_id=? AND COALESCE(payload_digest,'')=''`), id, b.TaskID, b.SessionID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	messages, _, err := scanMessageRows(rows, 0)
	if err != nil || len(messages) != 1 {
		return nil, errors.New("exact inline answer evidence unavailable")
	}
	if len(messages[0].Content) > 65536 {
		return nil, errors.New("answer evidence exceeds bound")
	}
	return messages[0], nil
}

func (r *Repository) guardOwnAnswerAmbiguity(ctx context.Context, tx *sqlx.Tx, b ownanswer.OwnAnswerBinding, qm, am *models.Message) error {
	rows, err := tx.QueryxContext(ctx, r.db.Rebind(`SELECT id,task_session_id,task_id,turn_id,author_type,author_id,'',requests_input,type,metadata,created_at,updated_at FROM task_session_messages WHERE task_id=? AND task_session_id=? AND type='clarification_request' ORDER BY created_at,id LIMIT 1025`), b.TaskID, b.SessionID)
	if err != nil {
		return err
	}
	messages, _, err := scanMessageRows(rows, 0)
	_ = rows.Close()
	if err != nil || len(messages) > 1024 {
		return errors.New("question correlation exceeds bound")
	}
	original, originalContext, err := ownanswer.OwnQuestionEnvelope(qm)
	if err != nil {
		return err
	}
	original.ID = ""
	bundle := 0
	for _, m := range messages {
		if m.Metadata["pending_id"] == b.PendingID {
			bundle++
		}
		if m.ID == qm.ID || m.CreatedAt.After(am.CreatedAt) {
			continue
		}
		q, c, e := ownanswer.OwnQuestionEnvelope(m)
		if e != nil {
			return errors.New("question correlation metadata is unclassified")
		}
		q.ID = ""
		if c == originalContext && q.Title == original.Title && q.Prompt == original.Prompt {
			return errors.New("human answer matches multiple question identities")
		}
	}
	if bundle != 1 {
		return errors.New("single-question bundle required")
	}
	return nil
}

// Native admission evidence is read only: no queue entry is consumed or replayed.
func (r *Repository) verifyOwnHumanAdmission(ctx context.Context, tx *sqlx.Tx, b ownanswer.OwnAnswerBinding, am *models.Message, receipt *ownanswer.OwnAnswerReceipt) error {
	raw, err := json.Marshal(am.Metadata[messagequeue.MetadataQueueAdmissionIDs])
	var ids []string
	if err != nil || json.Unmarshal(raw, &ids) != nil || len(ids) != 1 {
		return errors.New("native admission identity unavailable")
	}
	var incarnation, response string
	var at time.Time
	query := `SELECT a.session_incarnation_id,a.response_json,a.admitted_at FROM queue_admission_receipts a JOIN task_sessions s ON s.id=a.session_id AND s.task_id=a.task_id WHERE a.task_id=? AND a.session_id=? AND a.client_queue_id=? AND a.session_incarnation_id=s.queue_incarnation_id`
	if err = tx.QueryRowContext(ctx, r.db.Rebind(query), b.TaskID, b.SessionID, ids[0]).Scan(&incarnation, &response, &at); err != nil || len(response) > 65536 {
		return errors.New("durable current-incarnation Human admission unavailable")
	}
	var queued messagequeue.QueuedMessage
	if json.Unmarshal([]byte(response), &queued) != nil || queued.TaskID != b.TaskID || queued.SessionID != b.SessionID || queued.QueuedBy != messagequeue.QueuedByUser || queued.Content != am.Content || !at.Before(am.CreatedAt) {
		return errors.New("native Human admission evidence differs")
	}
	if len(queued.Metadata) != 1 {
		return errors.New("native admission origin is unclassified")
	}
	queueIDs, _ := json.Marshal(queued.Metadata[messagequeue.MetadataQueueAdmissionIDs])
	if string(queueIDs) != string(raw) {
		return errors.New("native admission linkage differs")
	}
	receipt.Workspace = b.WorkspaceID
	receipt.SessionIncarnationID = incarnation
	receipt.AdmissionID = ids[0]
	receipt.AdmissionSHA256 = ownanswer.OwnAnswerDigest([]byte(response))
	return nil
}

func (r *Repository) guardOwnAnswerScope(ctx context.Context, tx *sqlx.Tx, b ownanswer.OwnAnswerBinding) error {
	if err := r.guardActivePlanCommentTaskTx(ctx, tx, b.TaskID); err != nil {
		return err
	}
	if err := lockSessionTurnWrites(ctx, tx, r.db.DriverName(), b.SessionID); err != nil {
		return err
	}
	if err := plancommenttx.ValidateTargetSession(ctx, tx, r.db, b.TaskID, b.SessionID, false, ""); err != nil {
		return err
	}
	var workspace string
	if err := tx.GetContext(ctx, &workspace, r.db.Rebind(`SELECT workspace_id FROM tasks WHERE id=?`), b.TaskID); err != nil || workspace != b.WorkspaceID || workspace == "" {
		return errors.New("answer workspace differs")
	}
	return nil
}

func (r *Repository) persistOwnAnswerBinding(ctx context.Context, tx *sqlx.Tx, b ownanswer.OwnAnswerBinding, qm, am *models.Message, answer *ownanswer.Answer, receipt *ownanswer.OwnAnswerReceipt) error {
	qm.Metadata["status"] = clarificationStatusAnswered
	qm.Metadata["response"] = answer
	qm.Metadata[ownanswer.OwnAnswerReceiptKey] = receipt
	am.Metadata[ownanswer.OwnAnswerBindingKey] = receipt.ID
	for _, m := range []*models.Message{qm, am} {
		original := m.UpdatedAt
		raw, marshalErr := json.Marshal(m.Metadata)
		if marshalErr != nil {
			return marshalErr
		}
		m.UpdatedAt = receipt.BoundAt
		result, updateErr := tx.ExecContext(ctx, r.db.Rebind(`UPDATE task_session_messages SET metadata=?,updated_at=? WHERE id=? AND task_id=? AND task_session_id=? AND updated_at=?`), string(raw), m.UpdatedAt, m.ID, b.TaskID, b.SessionID, original)
		if updateErr != nil {
			return updateErr
		}
		n, countErr := result.RowsAffected()
		if countErr != nil || n != 1 {
			return errors.New("answer evidence changed concurrently")
		}
	}
	return nil
}

func guardOwnAnswerUnclaimed(qm, am *models.Message) error {
	if qm.Metadata["status"] != clarificationStatusPending || qm.Metadata["agent_disconnected"] != true || qm.Metadata["response"] != nil || qm.Metadata["clarification_response_delivery_pending"] != nil || am.Metadata[ownanswer.OwnAnswerBindingKey] != nil {
		return errors.New("question is not an unclaimed disconnected request")
	}
	return nil
}
