package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kandev/kandev/internal/clarification"
	mcpscope "github.com/kandev/kandev/internal/mcp/scope"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/ownanswer"
	ws "github.com/kandev/kandev/pkg/websocket"
)

// Serializes durable correlation and publication for the single backend owner.
// It is released before any Human wait. Restarts deduplicate from durable rows.
var ownQuestionCreationMu sync.Mutex

type ownQuestionReader interface {
	FindOwnClarificationMessages(context.Context, string, string, []string) ([]*models.Message, error)
}

type ownQuestionRequest struct {
	TaskID      string   `json:"task_id"`
	SessionID   string   `json:"session_id"`
	QuestionIDs []string `json:"question_ids"`
}

type ownQuestionItem struct {
	QuestionID        string                      `json:"question_id"`
	PendingID         string                      `json:"pending_id,omitempty"`
	RetryAllowed      bool                        `json:"retry_allowed"`
	MessageID         string                      `json:"message_id,omitempty"`
	TurnID            string                      `json:"requesting_turn_id,omitempty"`
	Status            string                      `json:"status"`
	CreatedAt         time.Time                   `json:"created_at,omitzero"`
	UpdatedAt         time.Time                   `json:"updated_at,omitzero"`
	AgentDisconnected bool                        `json:"agent_disconnected,omitempty"`
	QuestionSHA256    string                      `json:"question_sha256,omitempty"`
	Answer            *clarification.Answer       `json:"answer,omitempty"`
	AnswerReceipt     *ownanswer.OwnAnswerReceipt `json:"answer_receipt,omitempty"`
}

type ownQuestionReceipt struct {
	TaskID       string            `json:"task_id"`
	SessionID    string            `json:"session_id"`
	PendingID    string            `json:"pending_id,omitempty"`
	Status       string            `json:"status"`
	RetryAllowed bool              `json:"retry_allowed"`
	Questions    []ownQuestionItem `json:"questions"`
}

func validateQuestionIDs(ids []string) error {
	if len(ids) < 1 || len(ids) > 4 {
		return errors.New("question_ids requires 1-4 stable IDs")
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || len(id) > 128 || seen[id] {
			return errors.New("question IDs must be nonempty, unique, and at most 128 bytes")
		}
		seen[id] = true
	}
	return nil
}

func (h *Handlers) authorizeOwnQuestion(ctx context.Context, req ownQuestionRequest) (*models.Task, error) {
	p, ok := mcpscope.PrincipalFromContext(ctx)
	if !ok || p.IsAutomation() || req.SessionID != p.CallerSessionID || (req.TaskID != "" && req.TaskID != p.CallerTaskID) {
		return nil, errors.New("own-question caller mismatch")
	}
	caller := h.authorizedQueueCaller(ctx)
	if caller == nil {
		return nil, errors.New("own-question caller unavailable")
	}
	session, err := h.sessionRepo.GetTaskSession(ctx, p.CallerSessionID)
	if err != nil || session == nil || (session.State == models.TaskSessionStateCompleted || session.State == models.TaskSessionStateFailed || session.State == models.TaskSessionStateCancelled) {
		return nil, errors.New("own-question session is not active")
	}
	return caller, nil
}

func (h *Handlers) handleGetOwnQuestionStatus(ctx context.Context, msg *ws.Message) (*ws.Message, error) {
	var req ownQuestionRequest
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeBadRequest, "invalid payload", nil)
	}
	caller, err := h.authorizeOwnQuestion(ctx, req)
	if err != nil {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeForbidden, "exact own-task/session question access required", nil)
	}
	if err := validateQuestionIDs(req.QuestionIDs); err != nil {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeValidation, err.Error(), nil)
	}
	receipt, _, err := h.readOwnQuestion(ctx, caller.ID, req.SessionID, req.QuestionIDs)
	if err != nil {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeConflict, "own-question status unavailable or ambiguous", nil)
	}
	return ws.NewResponse(msg.ID, msg.Action, receipt)
}

func (h *Handlers) readOwnQuestion(ctx context.Context, taskID, sessionID string, ids []string) (*ownQuestionReceipt, []*models.Message, error) {
	reader, ok := h.sessionRepo.(ownQuestionReader)
	if !ok {
		return nil, nil, errors.New("own-question reader unavailable")
	}
	messages, err := reader.FindOwnClarificationMessages(ctx, taskID, sessionID, ids)
	if err != nil {
		return nil, nil, err
	}
	receipt := &ownQuestionReceipt{TaskID: taskID, SessionID: sessionID, Status: "not_created", RetryAllowed: true, Questions: []ownQuestionItem{}}
	seen := map[string]bool{}
	for _, id := range ids {
		seen[id] = false
	}
	for _, m := range messages {
		if err := addOwnQuestionItem(receipt, m, seen, taskID, sessionID); err != nil {
			return nil, nil, err
		}
	}
	completeOwnQuestionBatch(receipt, ids)
	return receipt, messages, nil
}

// Every requested ID has one result, in caller order. A missing ID has no
// durable identity; existing IDs retain their own immutable bundle identity.
func completeOwnQuestionBatch(r *ownQuestionReceipt, ids []string) {
	byID := make(map[string]ownQuestionItem, len(r.Questions))
	bundles := map[string]bool{}
	for _, item := range r.Questions {
		byID[item.QuestionID] = item
		bundles[item.PendingID] = true
	}
	r.Questions = make([]ownQuestionItem, 0, len(ids))
	for _, id := range ids {
		item, exists := byID[id]
		if !exists {
			item = ownQuestionItem{QuestionID: id, Status: "not_created", RetryAllowed: true}
		} else {
			r.RetryAllowed = false
		}
		r.Questions = append(r.Questions, item)
	}
	if len(bundles) == 1 {
		for pending := range bundles {
			r.PendingID = pending
		}
	}
	r.Status = ownBundleStatus(r.Questions)
}

func addOwnQuestionItem(r *ownQuestionReceipt, m *models.Message, seen map[string]bool, taskID, sessionID string) error {
	if m.TaskID != taskID || m.TaskSessionID != sessionID || m.Type != models.MessageTypeClarificationRequest || m.AuthorType != models.MessageAuthorAgent {
		return errors.New("question scope mismatch")
	}
	id, _ := m.Metadata["question_id"].(string)
	pending, _ := m.Metadata["pending_id"].(string)
	if _, requested := seen[id]; !requested || id == "" || pending == "" || seen[id] {
		return errors.New("ambiguous question identity")
	}
	seen[id] = true
	raw, err := json.Marshal(m.Metadata)
	if err != nil || len(raw) > 65536 {
		return errors.New("question metadata exceeds bound")
	}
	question, err := json.Marshal(m.Metadata["question"])
	if err != nil {
		return err
	}
	digest := sha256.Sum256(question)
	status, _ := m.Metadata["status"].(string)
	disconnected, _ := m.Metadata["agent_disconnected"].(bool)
	item := ownQuestionItem{QuestionID: id, PendingID: pending, MessageID: m.ID, TurnID: m.TurnID, Status: status, CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt, AgentDisconnected: disconnected, QuestionSHA256: hex.EncodeToString(digest[:])}
	item.Answer, item.Status, err = projectOwnAnswer(status, m.Metadata["response"], id)
	if err != nil {
		return err
	}
	if rawReceipt := m.Metadata[ownanswer.OwnAnswerReceiptKey]; rawReceipt != nil {
		data, e := json.Marshal(rawReceipt)
		if e != nil {
			return e
		}
		var parsed ownanswer.OwnAnswerReceipt
		if e = json.Unmarshal(data, &parsed); e != nil {
			return e
		}
		item.AnswerReceipt = &parsed
	}
	r.Questions = append(r.Questions, item)
	return nil
}

func ownBundleStatus(items []ownQuestionItem) string {
	status := items[0].Status
	for _, item := range items {
		if item.Status != status {
			return "mixed"
		}
	}
	return status
}

// prepareOwnQuestion preserves old identities, terminal answers, and pending
// Human input. Reusing a stable ID for a different question is a conflict.
func (h *Handlers) prepareOwnQuestion(ctx context.Context, taskID, sessionID string, questions []clarification.Question, questionContext string, liveWait bool) (string, *ownQuestionReceipt, error) {
	ownQuestionCreationMu.Lock()
	defer ownQuestionCreationMu.Unlock()
	ids := make([]string, len(questions))
	for i, q := range questions {
		ids[i] = q.ID
	}
	if err := validateQuestionIDs(ids); err != nil {
		return "", nil, err
	}
	receipt, messages, err := h.readOwnQuestion(ctx, taskID, sessionID, ids)
	if err != nil {
		return "", nil, err
	}
	if len(messages) > 0 {
		if len(messages) != len(ids) || receipt.PendingID == "" {
			return "", nil, errors.New("incomplete or distinct question request identities")
		}
		if err := matchOwnQuestionRetry(messages, questions, questionContext); err != nil {
			return "", nil, err
		}
		return receipt.PendingID, receipt, nil
	}
	if h.messageCreator == nil || h.clarificationSvc == nil {
		return "", nil, errors.New("clarification publication unavailable")
	}
	pending := uuid.New().String()
	if liveWait {
		pending, _ = h.clarificationSvc.CreateRequest(&clarification.Request{TaskID: taskID, SessionID: sessionID, Questions: questions, Context: questionContext})
	}
	if _, err := h.messageCreator.CreateClarificationRequestMessages(ctx, taskID, sessionID, pending, questions, questionContext); err != nil {
		if liveWait {
			h.clarificationSvc.CancelRequest(pending)
		}
		return "", nil, errors.New("clarification publication failed; correlate status before retry")
	}
	return pending, nil, nil
}

func matchOwnQuestionRetry(messages []*models.Message, questions []clarification.Question, questionContext string) error {
	byID := map[string]clarification.Question{}
	for _, q := range questions {
		byID[q.ID] = q
	}
	for _, m := range messages {
		id, _ := m.Metadata["question_id"].(string)
		data, err := json.Marshal(m.Metadata["question"])
		if err != nil {
			return err
		}
		var original clarification.Question
		if err := json.Unmarshal(data, &original); err != nil {
			return err
		}
		contextText, _ := m.Metadata["context"].(string)
		if !reflect.DeepEqual(original, byID[id]) || contextText != questionContext {
			return fmt.Errorf("stable question ID %s already identifies a different request", id)
		}
	}
	return nil
}

func (h *Handlers) handleReceiptUserQuestion(ctx context.Context, msg *ws.Message, taskID, sessionID string, questions []clarification.Question, questionContext string) (*ws.Message, error) {
	caller, err := h.authorizeOwnQuestion(ctx, ownQuestionRequest{TaskID: taskID, SessionID: sessionID})
	if err != nil {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeForbidden, "exact own-task/session question access required", nil)
	}
	ids := make([]string, len(questions))
	for i, q := range questions {
		ids[i] = q.ID
	}
	if err := validateQuestionIDs(ids); err != nil {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeValidation, err.Error(), nil)
	}
	if message := clarification.NormalizeAndValidateQuestions(questions); message != "" {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeValidation, message, nil)
	}
	_, existing, err := h.prepareOwnQuestion(ctx, caller.ID, sessionID, questions, questionContext, false)
	if err != nil {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeConflict, "question publication conflict; correlate own status before retry", nil)
	}
	if existing != nil {
		return ws.NewResponse(msg.ID, msg.Action, existing)
	}
	h.setSessionWaitingForInput(ctx, caller.ID, sessionID)
	receipt, _, err := h.readOwnQuestion(ctx, caller.ID, sessionID, ids)
	if err != nil {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeInternalError, "question accepted; read own status before retry", nil)
	}
	return ws.NewResponse(msg.ID, msg.Action, receipt)
}

func (h *Handlers) prepareQuestionPublication(ctx context.Context, taskID, sessionID string, questions []clarification.Question, questionContext string) (string, *ownQuestionReceipt, error) {
	if _, trusted := mcpscope.PrincipalFromContext(ctx); trusted && hasDurableQuestionIdentity(questions) {
		caller, err := h.authorizeOwnQuestion(ctx, ownQuestionRequest{TaskID: taskID, SessionID: sessionID})
		if err != nil {
			return "", nil, err
		}
		return h.prepareOwnQuestion(ctx, caller.ID, sessionID, questions, questionContext, true)
	}
	// Preserve the existing internal/configuration entrypoint. The new receipt
	// and readback endpoints always require a trusted task-session principal.
	req := &clarification.Request{TaskID: taskID, SessionID: sessionID, Questions: questions, Context: questionContext}
	pending, isNew := h.clarificationSvc.CreateRequest(req)
	if isNew && h.messageCreator != nil {
		if _, err := h.messageCreator.CreateClarificationRequestMessages(ctx, taskID, sessionID, pending, questions, questionContext); err != nil {
			h.clarificationSvc.CancelRequest(pending)
			return "", nil, err
		}
	}
	return pending, nil, nil
}

// Legacy autogenerated q1/q2 identities remain local to that interaction.
// Receipt mode requires explicit IDs and always deduplicates them.
func hasDurableQuestionIdentity(questions []clarification.Question) bool {
	for i, q := range questions {
		if q.ID == fmt.Sprintf("q%d", i+1) {
			return false
		}
	}
	return true
}

func recordedOwnAnswer(raw any, id string) (*clarification.Answer, error) {
	if raw == nil {
		return nil, nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var answer clarification.Answer
	if err := json.Unmarshal(data, &answer); err != nil || answer.QuestionID != id {
		return nil, errors.New("invalid own-question answer")
	}
	return &answer, nil
}

func projectOwnAnswer(status string, raw any, id string) (*clarification.Answer, string, error) {
	if status == "" {
		status = string(clarification.StatusPending)
	}
	switch status {
	case string(clarification.StatusPending), string(clarification.StatusAnswered), string(clarification.StatusRejected), string(clarification.StatusExpired), string(clarification.StatusCancelled):
	default:
		status = "unknown"
	}
	if status != string(clarification.StatusAnswered) {
		return nil, status, nil
	}
	answer, err := recordedOwnAnswer(raw, id)
	if answer == nil {
		status = "unknown"
	}
	return answer, status, err
}
