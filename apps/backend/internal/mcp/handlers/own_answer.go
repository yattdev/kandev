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

func (h *Handlers) handleReconcileOwnAnswer(ctx context.Context, msg *ws.Message) (*ws.Message, error) {
	var request struct {
		ownanswer.OwnAnswerBinding
		ResultMode string `json:"result_mode,omitempty"`
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
	writer, ok := h.sessionRepo.(ownAnswerWriter)
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
	rows, changed, err := writer.ReconcileOwnQuestionAnswer(ctx, b)
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

func validOwnAnswerMode(mode string) bool {
	return mode == "" || mode == ownanswer.DispositionMode
}
