package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/kandev/kandev/internal/clarification"
	mcpprofile "github.com/kandev/kandev/internal/mcp/profile"
	mcpscope "github.com/kandev/kandev/internal/mcp/scope"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/ownanswer"
	ws "github.com/kandev/kandev/pkg/websocket"
)

type ownAnswerWriter interface {
	ReconcileOwnQuestionAnswer(context.Context, ownanswer.OwnAnswerBinding) ([]*models.Message, bool, error)
}

type approvalLinkConsumer interface {
	ConsumeApprovalLinkGrant(context.Context, ownanswer.OwnAnswerBinding, string, string, string) ([]*models.Message, bool, error)
}

func (h *Handlers) handleReconcileOwnAnswer(ctx context.Context, msg *ws.Message) (*ws.Message, error) {
	var request struct {
		ownanswer.OwnAnswerBinding
		ResultMode  string `json:"result_mode,omitempty"`
		GrantID     string `json:"approval_link_grant_id,omitempty"`
		OperationID string `json:"approval_link_operation_id,omitempty"`
	}
	decoder := json.NewDecoder(bytes.NewReader(msg.Payload))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || request.Validate() != nil || !validOwnAnswerMode(request.ResultMode) {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeValidation, "exact question and Human message identities/hashes required", nil)
	}
	b := request.OwnAnswerBinding
	caller, err := h.authorizeOwnQuestion(ctx, ownQuestionRequest{TaskID: b.TaskID, SessionID: b.SessionID})
	if err != nil {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeForbidden, "exact own-task/session answer reconciliation required", nil)
	}
	p, _ := mcpscope.PrincipalFromContext(ctx)
	if p.Surface != mcpprofile.SurfaceKanbanTask {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeForbidden, "ordinary own-task question reconciliation required", nil)
	}
	b.TaskID = caller.ID
	b.WorkspaceID = p.WorkspaceID
	_, ok := h.sessionRepo.(ownAnswerWriter)
	if !ok || h.taskSvc == nil {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeInternalError, "answer reconciliation unavailable", nil)
	}
	store, ok := h.clarificationSvc.(interface {
		GetRequest(string) (*clarification.Request, bool)
	})
	if !ok {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeConflict, "live waiter state unavailable", nil)
	}
	if _, live := store.GetRequest(b.PendingID); live {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeConflict, "question still has a live waiter", nil)
	}
	rows, changed, err := h.reconcileOwnAnswerEvidence(ctx, b, request.GrantID, request.OperationID)
	if errors.Is(err, ownanswer.ErrAnswerFormatUnsupported) && request.ResultMode == ownanswer.DispositionMode {
		return ws.NewResponse(msg.ID, msg.Action, unsupportedOwnAnswerDisposition(b))
	}
	if err != nil {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeConflict, "stored evidence cannot be securely correlated; preserve the original question and Human message", nil)
	}
	// Publication updates normal summaries/UI only; never submit or replay a prompt.
	publishErr := h.taskSvc.PublishClarificationBundleUpdates(ctx, rows)
	receipt, _, err := h.readOwnQuestion(ctx, b.TaskID, b.SessionID, []string{b.QuestionID})
	if err != nil {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeInternalError, "answer durably bound; read own status before retry", nil)
	}
	return ws.NewResponse(msg.ID, msg.Action, map[string]any{"receipt": receipt, "changed": changed, "publication_pending": publishErr != nil})
}

func (h *Handlers) reconcileOwnAnswerEvidence(ctx context.Context, b ownanswer.OwnAnswerBinding, grant, operation string) ([]*models.Message, bool, error) {
	if grant == "" && operation == "" {
		writer, ok := h.sessionRepo.(ownAnswerWriter)
		if !ok {
			return nil, false, errors.New("answer reconciliation unavailable")
		}
		return writer.ReconcileOwnQuestionAnswer(ctx, b)
	}
	consumer, supported := h.sessionRepo.(approvalLinkConsumer)
	session, err := h.sessionRepo.GetTaskSession(ctx, b.SessionID)
	if !supported || err != nil || session == nil {
		return nil, false, errors.New("approval-link consumption unavailable")
	}
	return consumer.ConsumeApprovalLinkGrant(ctx, b, grant, operation, session.QueueIncarnationID)
}

func validOwnAnswerMode(mode string) bool {
	return mode == "" || mode == ownanswer.DispositionMode
}
