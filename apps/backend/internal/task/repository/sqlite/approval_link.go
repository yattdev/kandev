package sqlite

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/kandev/kandev/internal/agentctl/types/streams"
	"github.com/kandev/kandev/internal/orchestrator/messagequeue"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/ownanswer"
)

var errApprovalLink = errors.New("approval-link evidence or authorization unavailable")
var approvalOperationPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{8,128}$`)

// Issuance is closed to the reviewed exact case.
func (r *Repository) CreateApprovalLinkGrant(ctx context.Context, req ownanswer.ApprovalLinkRequest) (*ownanswer.ApprovalLinkGrant, error) {
	if req.OwnAnswerBinding != ownanswer.ApprovalLinkCase() {
		return nil, errApprovalLink
	}
	return r.createApprovalLinkGrant(ctx, req)
}

func (r *Repository) createApprovalLinkGrant(ctx context.Context, req ownanswer.ApprovalLinkRequest) (*ownanswer.ApprovalLinkGrant, error) {
	creator, err := ownanswer.BrowserGrantCreator(ctx)
	if err != nil {
		return nil, err
	}
	b := req.OwnAnswerBinding
	if b.Validate() != nil {
		return nil, errApprovalLink
	}
	tx, release, err := r.beginPlanCommentTx(ctx, b.TaskID)
	if err != nil {
		return nil, err
	}
	defer release()
	defer func() { _ = tx.Rollback() }()
	q, a, incarnation, err := r.approvalLinkEvidence(ctx, tx, b)
	if err != nil {
		return nil, err
	}
	if err = guardApprovalLinkUnclaimed(q, a); err != nil {
		return nil, err
	}
	if !a.CreatedAt.Equal(a.UpdatedAt) {
		return nil, errApprovalLink
	}
	if err = r.guardOwnAnswerAmbiguity(ctx, tx, b, q, a); err != nil {
		return nil, err
	}
	if err = approvalLinkOption(q, req.OptionID); err != nil {
		return nil, err
	}
	qhash, err := ownanswer.ApprovalEvidenceDigest(q, q.UpdatedAt, false)
	if err != nil {
		return nil, err
	}
	ahash, err := ownanswer.ApprovalEvidenceDigest(a, a.UpdatedAt, false)
	if err != nil {
		return nil, err
	}
	receiptHash, err := r.approvalOriginalReceipt(ctx, tx, b, a)
	if err != nil {
		return nil, err
	}
	now := r.nowUTC()
	authority, err := r.approvalAuthorityDigest(ctx, tx, b, creator.UserID, creator.SessionID, creator.OrgID)
	if err != nil {
		return nil, err
	}
	g := &ownanswer.ApprovalLinkGrant{ID: uuid.NewString(), Purpose: ownanswer.ApprovalLinkPurpose, ApprovalLinkRequest: req,
		Workspace: b.WorkspaceID, Incarnation: incarnation, CreatorUserID: creator.UserID, CreatorSessionID: creator.SessionID,
		CreatorOrgID: creator.OrgID, CreatorAuthoritySHA256: authority,
		IssuedAt: now, ExpiresAt: now.Add(15 * time.Minute), QuestionEvidenceSHA256: qhash, AnswerEvidenceSHA256: ahash,
		OriginalReceiptSHA256: receiptHash, QuestionUpdatedAt: q.UpdatedAt, AnswerUpdatedAt: a.UpdatedAt}
	if err = r.insertApprovalLinkGrant(ctx, tx, b, g); err != nil {
		return nil, err
	}
	return g, nil
}

func (r *Repository) insertApprovalLinkGrant(ctx context.Context, tx *sqlx.Tx, b ownanswer.OwnAnswerBinding, g *ownanswer.ApprovalLinkGrant) error {
	raw, err := json.Marshal(g)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, r.db.Rebind(`INSERT INTO own_answer_approval_link_grants(id,task_id,session_id,question_message_id,grant_json,state) VALUES (?,?,?,?,?,'issued')`), g.ID, b.TaskID, b.SessionID, b.QuestionMessageID, string(raw))
	if err != nil {
		return err
	}
	if err = r.checkApprovalAuthority(ctx, tx, b, g); err != nil {
		return err
	}
	return tx.Commit()
}

func guardApprovalLinkUnclaimed(q, a *models.Message) error {
	if err := guardOwnAnswerUnclaimed(q, a); err != nil {
		return err
	}
	for _, key := range []string{"response", ownanswer.OwnAnswerReceiptKey} {
		if _, present := q.Metadata[key]; present {
			return errApprovalLink
		}
	}
	if _, present := a.Metadata[ownanswer.OwnAnswerBindingKey]; present {
		return errApprovalLink
	}
	return nil
}

func (r *Repository) approvalLinkEvidence(ctx context.Context, tx *sqlx.Tx, b ownanswer.OwnAnswerBinding) (*models.Message, *models.Message, string, error) {
	if err := r.guardOwnAnswerScope(ctx, tx, b); err != nil {
		return nil, nil, "", err
	}
	q, err := r.loadOwnAnswerMessage(ctx, tx, b, b.QuestionMessageID)
	if err != nil {
		return nil, nil, "", err
	}
	a, err := r.loadOwnAnswerMessage(ctx, tx, b, b.AnswerMessageID)
	if err != nil {
		return nil, nil, "", err
	}
	raw, err := json.Marshal(q.Metadata["question"])
	if err != nil || ownanswer.OwnAnswerDigest(raw) != b.QuestionSHA256 || ownanswer.OwnAnswerDigest([]byte(a.Content)) != b.AnswerSHA256 {
		return nil, nil, "", errApprovalLink
	}
	if !approvalQuestionMatches(q, b) || !approvalAnswerMatches(a, q) {
		return nil, nil, "", errApprovalLink
	}
	var incarnation string
	err = tx.GetContext(ctx, &incarnation, r.db.Rebind(`SELECT queue_incarnation_id FROM task_sessions WHERE id=? AND task_id=?`), b.SessionID, b.TaskID)
	if err != nil || incarnation == "" {
		return nil, nil, "", errApprovalLink
	}
	return q, a, incarnation, nil
}

func approvalQuestionMatches(q *models.Message, b ownanswer.OwnAnswerBinding) bool {
	return q.AuthorType == models.MessageAuthorAgent && q.Type == models.MessageTypeClarificationRequest && q.RequestsInput && q.Metadata["question_id"] == b.QuestionID && q.Metadata["pending_id"] == b.PendingID
}

func approvalAnswerMatches(a, q *models.Message) bool {
	return a.AuthorType == models.MessageAuthorUser && a.Type == models.MessageTypeMessage && !a.RequestsInput && a.TurnID != "" && a.CreatedAt.After(q.CreatedAt)
}

func approvalLinkOption(qm *models.Message, option string) error {
	q, _, err := ownanswer.OwnQuestionEnvelope(qm)
	if err != nil || q.ID != qm.Metadata["question_id"] || option == "" {
		return errApprovalLink
	}
	seen := map[string]bool{}
	found := false
	for _, o := range q.Options {
		if o.ID == "" || seen[o.ID] || o.Label == "" {
			return errApprovalLink
		}
		seen[o.ID] = true
		found = found || o.ID == option
	}
	if !found {
		return errApprovalLink
	}
	return nil
}

// A stored ingress receipt is evidence to bind, never authority to create a
// grant. Direct native comments have no original question linkage; the grant
// explicitly records new authority instead of inferring one from their text.
func (r *Repository) approvalOriginalReceipt(ctx context.Context, tx *sqlx.Tx, b ownanswer.OwnAnswerBinding, a *models.Message) (string, error) {
	for key := range a.Metadata {
		if key != "queue_admission_ids" && key != "client_message_request_fingerprint" && key != ownanswer.OwnAnswerBindingKey {
			return "", errApprovalLink
		}
	}
	fp, ok := a.Metadata["client_message_request_fingerprint"].(string)
	decoded, decodeErr := hex.DecodeString(fp)
	if !ok || decodeErr != nil || len(decoded) != 32 {
		return "", errApprovalLink
	}
	if a.Metadata["queue_admission_ids"] != nil {
		receipt := &ownanswer.OwnAnswerReceipt{}
		if err := r.verifyOwnHumanAdmission(ctx, tx, b, a, receipt); err != nil {
			return "", err
		}
		return r.approvalAdmissionDigest(ctx, tx, b, receipt.AdmissionID)
	}
	expected := 1
	if a.Metadata[ownanswer.OwnAnswerBindingKey] != nil {
		expected++
	}
	if len(a.Metadata) != expected {
		return "", errApprovalLink
	}
	return ownanswer.OwnAnswerDigest([]byte(fp)), nil
}

func (r *Repository) approvalAdmissionDigest(ctx context.Context, tx *sqlx.Tx, b ownanswer.OwnAnswerBinding, id string) (string, error) {
	var stored struct {
		Task        string    `db:"task_id"`
		Session     string    `db:"session_id"`
		Incarnation string    `db:"session_incarnation_id"`
		ClientID    string    `db:"client_queue_id"`
		Fingerprint string    `db:"request_fingerprint"`
		AcceptedID  string    `db:"accepted_queue_id"`
		Response    string    `db:"response_json"`
		AdmittedAt  time.Time `db:"admitted_at"`
	}
	err := tx.GetContext(ctx, &stored, r.db.Rebind(`SELECT task_id,session_id,session_incarnation_id,client_queue_id,request_fingerprint,accepted_queue_id,response_json,admitted_at FROM queue_admission_receipts WHERE task_id=? AND session_id=? AND client_queue_id=? AND session_incarnation_id=(SELECT queue_incarnation_id FROM task_sessions WHERE id=?)`), b.TaskID, b.SessionID, id, b.SessionID)
	var queued messagequeue.QueuedMessage
	fp, decodeErr := hex.DecodeString(stored.Fingerprint)
	if err != nil || decodeErr != nil || len(fp) != 32 || json.Unmarshal([]byte(stored.Response), &queued) != nil || stored.AcceptedID != queued.ID {
		return "", errApprovalLink
	}
	raw, err := json.Marshal(stored)
	return ownanswer.OwnAnswerDigest(raw), err
}

func validApprovalOperation(operation string) bool {
	return approvalOperationPattern.MatchString(operation)
}

func (r *Repository) ConsumeApprovalLinkGrant(ctx context.Context, b ownanswer.OwnAnswerBinding, id, operation, incarnation string) ([]*models.Message, bool, error) {
	e, ok := streams.MCPExecutionContextFromContext(ctx)
	if b != ownanswer.ApprovalLinkCase() || !ok || e.TaskID != b.TaskID || e.SessionID != b.SessionID || e.ExecutionID == "" {
		return nil, false, errApprovalLink
	}
	return r.consumeApprovalLinkGrant(ctx, b, id, operation, incarnation)
}

func (r *Repository) consumeApprovalLinkGrant(ctx context.Context, b ownanswer.OwnAnswerBinding, id, operation, incarnation string) ([]*models.Message, bool, error) {
	if !validApprovalConsumption(b, id, operation, incarnation) {
		return nil, false, errApprovalLink
	}
	tx, release, err := r.beginPlanCommentTx(ctx, b.TaskID)
	if err != nil {
		return nil, false, err
	}
	defer release()
	defer func() { _ = tx.Rollback() }()
	q, a, current, err := r.approvalLinkEvidence(ctx, tx, b)
	if err != nil || current != incarnation {
		return nil, false, errApprovalLink
	}
	fence, err := r.captureApprovalCommitFence(ctx, tx, b)
	if err != nil {
		return nil, false, err
	}
	g, state, priorOperation, err := r.readApprovalLinkGrant(ctx, tx, b, id)
	if err != nil {
		return nil, false, err
	}
	settled := state == "consumed"
	if err = r.checkApprovalLinkGrant(ctx, tx, b, g, q, a, incarnation, settled); err != nil {
		return nil, false, err
	}
	receipt := approvalLinkReceipt(g, b, operation)
	answer := &ownanswer.Answer{QuestionID: b.QuestionID, SelectedOptions: []string{g.OptionID}}
	if settled {
		if priorOperation != operation || !ownAnswerReplayMatches(q.Metadata[ownanswer.OwnAnswerReceiptKey], a, receipt, answer, q) {
			return nil, false, errApprovalLink
		}
		return []*models.Message{q}, false, r.commitApprovalLink(ctx, tx, b, fence, g)
	}
	if state != "issued" {
		return nil, false, errApprovalLink
	}
	if err = r.settleApprovalLink(ctx, tx, b, g, operation, q, a, answer, receipt); err != nil {
		return nil, false, err
	}
	if err = r.commitApprovalLink(ctx, tx, b, fence, g); err != nil {
		return nil, false, err
	}
	return []*models.Message{q}, true, nil
}

func validApprovalConsumption(b ownanswer.OwnAnswerBinding, id, operation, incarnation string) bool {
	return b.Validate() == nil && id != "" && len(id) <= 128 && validApprovalOperation(operation) && incarnation != ""
}

func (r *Repository) settleApprovalLink(ctx context.Context, tx *sqlx.Tx, b ownanswer.OwnAnswerBinding, g *ownanswer.ApprovalLinkGrant, operation string, q, a *models.Message, answer *ownanswer.Answer, receipt *ownanswer.OwnAnswerReceipt) error {
	if !r.nowUTC().Before(g.ExpiresAt) || r.nowUTC().Before(g.IssuedAt) {
		return errApprovalLink
	}
	var err error
	if err = guardOwnAnswerUnclaimed(q, a); err != nil {
		return err
	}
	if err = r.guardOwnAnswerAmbiguity(ctx, tx, b, q, a); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, r.db.Rebind(`UPDATE own_answer_approval_link_grants SET state='consumed',operation_id=? WHERE id=? AND state='issued' AND operation_id=''`), operation, g.ID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return errApprovalLink
	}
	receipt.BoundAt = r.nowUTC()
	return r.persistOwnAnswerBinding(ctx, tx, b, q, a, answer, receipt)
}

func (r *Repository) readApprovalLinkGrant(ctx context.Context, tx *sqlx.Tx, b ownanswer.OwnAnswerBinding, id string) (*ownanswer.ApprovalLinkGrant, string, string, error) {
	var raw, state, operation string
	err := tx.QueryRowContext(ctx, r.db.Rebind(`SELECT grant_json,state,operation_id FROM own_answer_approval_link_grants WHERE id=? AND task_id=? AND session_id=? AND question_message_id=?`), id, b.TaskID, b.SessionID, b.QuestionMessageID).Scan(&raw, &state, &operation)
	var g ownanswer.ApprovalLinkGrant
	if err != nil || len(raw) > 16384 || json.Unmarshal([]byte(raw), &g) != nil || g.ID != id {
		return nil, "", "", errApprovalLink
	}
	g.WorkspaceID = g.Workspace
	return &g, state, operation, nil
}

func (r *Repository) checkApprovalLinkGrant(ctx context.Context, tx *sqlx.Tx, b ownanswer.OwnAnswerBinding, g *ownanswer.ApprovalLinkGrant, q, a *models.Message, incarnation string, settled bool) error {
	if g.OwnAnswerBinding != b || g.Incarnation != incarnation || g.Purpose != ownanswer.ApprovalLinkPurpose || g.CreatorUserID == "" || g.CreatorSessionID == "" || !g.ExpiresAt.After(g.IssuedAt) {
		return errApprovalLink
	}
	if err := approvalLinkOption(q, g.OptionID); err != nil {
		return err
	}
	qh, err := ownanswer.ApprovalEvidenceDigest(q, g.QuestionUpdatedAt, settled)
	if err != nil || qh != g.QuestionEvidenceSHA256 {
		return errApprovalLink
	}
	ah, err := ownanswer.ApprovalEvidenceDigest(a, g.AnswerUpdatedAt, settled)
	if err != nil || ah != g.AnswerEvidenceSHA256 {
		return errApprovalLink
	}
	hash, err := r.approvalOriginalReceipt(ctx, tx, b, a)
	if err != nil || hash != g.OriginalReceiptSHA256 {
		return errApprovalLink
	}
	return r.checkApprovalAuthority(ctx, tx, b, g)
}

func approvalLinkReceipt(g *ownanswer.ApprovalLinkGrant, b ownanswer.OwnAnswerBinding, operation string) *ownanswer.OwnAnswerReceipt {
	return &ownanswer.OwnAnswerReceipt{Version: "own-question-approval-link/v1", ID: g.ID, OwnAnswerBinding: b, Workspace: g.Workspace,
		SessionIncarnationID: g.Incarnation, AdmissionSHA256: g.OriginalReceiptSHA256, AnswerMetadataSHA256: g.AnswerEvidenceSHA256,
		Provenance: ownanswer.ApprovalLinkPurpose, ApprovalGrantID: g.ID, ApprovalOperationID: operation}
}
