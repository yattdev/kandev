package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/kandev/kandev/internal/db"
	mcpscope "github.com/kandev/kandev/internal/mcp/scope"
	"github.com/kandev/kandev/internal/orchestrator/messagequeue"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/plancomments"
	"github.com/kandev/kandev/internal/task/service"
	ws "github.com/kandev/kandev/pkg/websocket"
)

const taskMessageIntentKey = "task_message_intent_id"
const observeTaskMessageTurn = "observe_original_turn"

var taskMessageIntentNamespace = uuid.MustParse("2c81d732-93f2-48b8-b132-1fde6e603147")

type taskMessageReceiptRequest struct {
	TaskID            string `json:"task_id"`
	SessionID         string `json:"session_id"`
	IntentID          string `json:"intent_id"`
	MessageID         string `json:"message_id"`
	SenderTaskID      string `json:"sender_task_id"`
	SenderSessionID   string `json:"sender_session_id"`
	Prompt            string `json:"prompt"`
	DeliveryMode      string `json:"delivery_mode"`
	ReplyToQuestionID string `json:"reply_to_question_id"`
}

type taskMessageReceipt struct {
	TaskID          string `json:"task_id,omitempty"`
	SessionID       string `json:"session_id,omitempty"`
	IntentID        string `json:"intent_id,omitempty"`
	IntentMessageID string `json:"intent_message_id,omitempty"`
	MessageID       string `json:"message_id,omitempty"`
	QueueID         string `json:"queue_id,omitempty"`
	TurnID          string `json:"turn_id,omitempty"`
	Status          string `json:"status"`
	RetryAllowed    bool   `json:"retry_allowed"`
	ContinueAllowed bool   `json:"continue_allowed"`
	Continuation    string `json:"continuation"`
	ContentSHA256   string `json:"content_sha256,omitempty"`
	AdmissionError  string `json:"admission_error,omitempty"`
}

type taskMessageReceiptReader interface {
	FindOwnTaskMessageRecords(context.Context, string, string, string, string, string, string, string) ([]models.TaskMessageRecord, error)
}
type queuedTaskMessageNotifier interface {
	NotifyQueuedUserPrompt(context.Context, string, string)
}

func validateTaskMessageIntent(id string) error {
	if len(id) < 8 || len(id) > 128 {
		return errors.New("intent_id requires 8-128 ASCII identifier bytes")
	}
	for _, c := range []byte(id) {
		valid := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.'
		if !valid {
			return errors.New("invalid intent_id")
		}
	}
	return nil
}

func (h *Handlers) authorizeTaskMessageReceipt(ctx context.Context, req taskMessageReceiptRequest) (mcpscope.Principal, error) {
	p, ok := mcpscope.PrincipalFromContext(ctx)
	if !ok || p.IsAutomation() || req.SenderTaskID != p.CallerTaskID || req.SenderSessionID != p.CallerSessionID {
		return p, errors.New("exact task message sender required")
	}
	if _, err := h.authorizeOwnQuestion(ctx, ownQuestionRequest{TaskID: p.CallerTaskID, SessionID: p.CallerSessionID}); err != nil {
		return p, err
	}
	if req.IntentID != "" {
		if err := validateTaskMessageIntent(req.IntentID); err != nil {
			return p, err
		}
		if req.MessageID != "" || req.TaskID == "" || req.SessionID == "" {
			return p, errors.New("intent requires one exact target task/session")
		}
	} else {
		if _, err := uuid.Parse(req.MessageID); err != nil {
			return p, errors.New("exact legacy message UUID required")
		}
	}
	if (req.TaskID == "") != (req.SessionID == "") {
		return p, errors.New("target task/session must be paired")
	}
	if req.TaskID != "" {
		if _, _, err := h.taskMessageReceiptTarget(ctx, p, req.TaskID, req.SessionID); err != nil {
			return p, err
		}
	}
	return p, nil
}

func (h *Handlers) taskMessageReceiptTarget(ctx context.Context, p mcpscope.Principal, taskID, sessionID string) (*models.Task, *models.TaskSession, error) {
	task, err := h.taskSvc.GetTask(ctx, taskID)
	if err != nil || task == nil || task.WorkspaceID != p.WorkspaceID {
		return nil, nil, errors.New("task message target unavailable")
	}
	session, err := h.sessionRepo.GetTaskSession(ctx, sessionID)
	if err != nil || session == nil || session.TaskID != taskID || sessionID == p.CallerSessionID {
		return nil, nil, errors.New("task message session unavailable")
	}
	return task, session, nil
}

func taskMessageIntentMessageID(p mcpscope.Principal, req taskMessageReceiptRequest) string {
	raw, _ := json.Marshal([]string{p.CallerTaskID, p.CallerSessionID, req.TaskID, req.SessionID, req.IntentID})
	return uuid.NewSHA1(taskMessageIntentNamespace, raw).String()
}

func (h *Handlers) readTaskMessageReceipt(ctx context.Context, p mcpscope.Principal, req taskMessageReceiptRequest) (*taskMessageReceipt, []models.TaskMessageRecord, error) {
	reader, ok := h.sessionRepo.(taskMessageReceiptReader)
	if !ok {
		return nil, nil, errors.New("task message receipt reader unavailable")
	}
	records, err := reader.FindOwnTaskMessageRecords(ctx, p.WorkspaceID, p.CallerTaskID, p.CallerSessionID, req.TaskID, req.SessionID, req.IntentID, req.MessageID)
	if err != nil {
		return nil, nil, err
	}
	receipt := &taskMessageReceipt{TaskID: req.TaskID, SessionID: req.SessionID, IntentID: req.IntentID, Status: "not_created", RetryAllowed: req.IntentID != "", Continuation: observeTaskMessageTurn}
	if req.IntentID != "" {
		receipt.IntentMessageID = taskMessageIntentMessageID(p, req)
		receipt.Continuation = "publish_same_intent"
	}
	if len(records) == 0 {
		if req.IntentID == "" {
			receipt.Status = "not_found"
		}
		return receipt, records, nil
	}
	receipt.RetryAllowed = false
	receipt.Continuation = observeTaskMessageTurn
	seen := map[string]bool{}
	identity := ""
	for _, record := range records {
		if seen[record.Kind] || (identity != "" && identity != record.ID) {
			return nil, nil, errors.New("ambiguous task message intent")
		}
		seen[record.Kind] = true
		identity = record.ID
		receipt.TaskID = record.TaskID
		receipt.SessionID = record.SessionID
		receipt.ContentSHA256 = record.ContentSHA256
		if record.Kind == "message" {
			receipt.MessageID = record.ID
			receipt.TurnID = record.TurnID
			receipt.Status = "recorded_delivery_unknown"
		} else {
			receipt.QueueID = record.ID
		}
	}
	for _, record := range records {
		if record.Kind == "queue" {
			projectTaskMessageQueueReceipt(receipt, record)
		}
	}
	if receipt.ContinueAllowed {
		if err := h.validateTaskMessageContinuation(ctx, p, receipt, records); err != nil {
			receipt.ContinueAllowed = false
			receipt.Continuation = observeTaskMessageTurn
		}
	}
	return receipt, records, nil
}

func projectTaskMessageQueueReceipt(r *taskMessageReceipt, record models.TaskMessageRecord) {
	r.Status = taskMessageStatusQueued
	r.ContinueAllowed = true
	r.Continuation = "recheck_original_queue"
	if attempted, _ := record.Metadata[messagequeue.MetadataDeliveryAttempted].(bool); attempted {
		r.Status = "delivery_attempted"
		r.ContinueAllowed = false
		r.Continuation = observeTaskMessageTurn
		return
	}
	if reserved, _ := record.Metadata[messagequeue.MetadataLifecycleReserved].(bool); reserved {
		r.Status = "dispatching"
		r.ContinueAllowed = false
		r.Continuation = observeTaskMessageTurn
	}
}

func (h *Handlers) handleGetTaskMessageStatus(ctx context.Context, msg *ws.Message) (*ws.Message, error) {
	var req taskMessageReceiptRequest
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeValidation, "invalid receipt request", nil)
	}
	p, err := h.authorizeTaskMessageReceipt(ctx, req)
	if err != nil {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeForbidden, "authenticated same-workspace exact sender receipt required", nil)
	}
	receipt, records, err := h.readTaskMessageReceipt(ctx, p, req)
	if err != nil {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeConflict, "task message status unavailable or ambiguous; do not resend", nil)
	}
	if msg.Action == ws.ActionMCPContinueTaskMessage && receipt.ContinueAllowed {
		if err := h.continueTaskMessageReceipt(ctx, p, receipt, records); err != nil {
			return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeConflict, "original queue cannot safely continue; do not resend", nil)
		}
	}
	return ws.NewResponse(msg.ID, msg.Action, receipt)
}

func (h *Handlers) validateTaskMessageContinuation(ctx context.Context, p mcpscope.Principal, r *taskMessageReceipt, records []models.TaskMessageRecord) error {
	task, session, err := h.taskMessageReceiptTarget(ctx, p, r.TaskID, r.SessionID)
	if err != nil {
		return err
	}
	if task.ArchivedAt != nil || session.State == models.TaskSessionStateCancelled || session.State == models.TaskSessionStateCompleted || session.State == models.TaskSessionStateFailed {
		return errors.New("task message continuation owner inactive")
	}
	for _, record := range records {
		if step, ok := record.Metadata["task_message_target_step"].(string); ok && step != task.WorkflowStepID {
			return errors.New("original task message step changed")
		}
		if incarnation, ok := record.Metadata["task_message_target_incarnation"].(string); ok && incarnation != session.QueueIncarnationID {
			return errors.New("original session replaced")
		}
	}
	return nil
}

func (h *Handlers) continueTaskMessageReceipt(ctx context.Context, p mcpscope.Principal, r *taskMessageReceipt, records []models.TaskMessageRecord) error {
	if err := h.validateTaskMessageContinuation(ctx, p, r, records); err != nil {
		return err
	}
	notifier, ok := h.sessionLauncher.(queuedTaskMessageNotifier)
	if !ok {
		return errors.New("queue readiness unavailable")
	}
	notifyCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	notifier.NotifyQueuedUserPrompt(notifyCtx, r.TaskID, r.SessionID)
	return nil
}

func (h *Handlers) handleReceiptTaskMessage(ctx context.Context, msg *ws.Message) (*ws.Message, error) {
	var req taskMessageReceiptRequest
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeValidation, "invalid intent request", nil)
	}
	p, err := h.authorizeTaskMessageReceipt(ctx, req)
	if err != nil {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeForbidden, "authenticated same-workspace exact sender required", nil)
	}
	if req.Prompt == "" || len(req.Prompt) > 65536 || req.ReplyToQuestionID != "" || (req.DeliveryMode != "" && req.DeliveryMode != deliveryModeQueued) {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeValidation, "intent receipts require a bounded prompt and queued delivery without parent-question reply", nil)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	receipt, records, err := h.readTaskMessageReceipt(ctx, p, req)
	if err != nil {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeConflict, "correlate task message status before retry", nil)
	}
	fingerprint := taskMessageRequestFingerprint(req)
	if len(records) > 0 {
		return replayTaskMessageReceipt(msg, receipt, records, fingerprint)
	}
	if h.sessionLauncher == nil || h.sessionLauncher.GetMessageQueue() == nil {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeConflict, "durable queue unavailable", nil)
	}
	err = h.admitTaskMessageReceipt(ctx, p, req, receipt.IntentMessageID, fingerprint)
	readCtx, readCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer readCancel()
	receipt, readRecords, readErr := h.readTaskMessageReceipt(readCtx, p, req)
	if readErr != nil {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeConflict, "task message admission unconfirmed; read original intent before retry", nil)
	}
	if err != nil {
		receipt.AdmissionError = "admission_unavailable"
	} else if receipt.ContinueAllowed {
		_ = h.continueTaskMessageReceipt(readCtx, p, receipt, readRecords)
	}
	return ws.NewResponse(msg.ID, msg.Action, receipt)
}

func taskMessageRequestFingerprint(req taskMessageReceiptRequest) string {
	raw, _ := json.Marshal([]string{req.SenderTaskID, req.SenderSessionID, req.TaskID, req.SessionID, req.IntentID, req.Prompt})
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func replayTaskMessageReceipt(msg *ws.Message, r *taskMessageReceipt, records []models.TaskMessageRecord, fingerprint string) (*ws.Message, error) {
	for _, record := range records {
		if got, _ := record.Metadata[plancomments.MetadataClientMessageFingerprint].(string); got != "" && got != fingerprint {
			return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeConflict, "intent already identifies a different task message", nil)
		}
	}
	return ws.NewResponse(msg.ID, msg.Action, r)
}

func (h *Handlers) admitTaskMessageReceipt(ctx context.Context, p mcpscope.Principal, req taskMessageReceiptRequest, id, fingerprint string) error {
	task, session, err := h.taskMessageReceiptTarget(ctx, p, req.TaskID, req.SessionID)
	if err != nil {
		return err
	}
	sender, err := h.taskSvc.GetTask(ctx, p.CallerTaskID)
	if err != nil {
		return err
	}
	prompt := h.appendPromptReferenceExpansionContext(ctx, req.Prompt)
	prompt, metadata := wrapAgentMessage(prompt, sender, p.CallerSessionID, h.lookupSenderSessionName(ctx, p.CallerTaskID, p.CallerSessionID), task.ID == sender.ID)
	metadata[taskMessageIntentKey] = req.IntentID
	metadata[plancomments.MetadataClientMessageFingerprint] = fingerprint
	metadata["task_message_target_step"] = task.WorkflowStepID
	metadata["task_message_target_incarnation"] = session.QueueIncarnationID
	queueMetadata := cloneTaskMessageMetadataMap(metadata)
	queueMetadata[messagequeue.MetadataDurableTranscriptMessageID] = id
	queueMetadata["user_message_recorded"] = true
	queued := &messagequeue.QueuedMessage{ID: id, Metadata: queueMetadata}
	create := &service.CreateMessageRequest{TaskID: task.ID, TaskSessionID: session.ID, Content: prompt, AuthorType: string(models.MessageAuthorUser), Metadata: metadata, ExpectedSessionState: session.State}
	return retryTaskMessageWrite(ctx, func() error {
		_, err := h.taskSvc.CreateTaskMessageQueuedIdempotent(ctx, id, create, queued, h.sessionLauncher.GetMessageQueue().MaxPerSession())
		return err
	})
}

func retryTaskMessageWrite(ctx context.Context, write func() error) error {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if err = write(); err == nil || !db.IsTransientError(err) {
			return err
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return err
}
