package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"time"
	"unicode/utf8"

	"github.com/kandev/kandev/internal/auth/authn"
	mcpprofile "github.com/kandev/kandev/internal/mcp/profile"
	mcpscope "github.com/kandev/kandev/internal/mcp/scope"
	"github.com/kandev/kandev/internal/orchestrator/messagequeue"
	v1 "github.com/kandev/kandev/pkg/api/v1"
	ws "github.com/kandev/kandev/pkg/websocket"
	"go.uber.org/zap"
)

const censusWireLimit = 65536

type claimedQueueService interface {
	ObservePendingCensus(context.Context, messagequeue.QueueSessionIdentity, string) (messagequeue.PendingCensus, error)
	RemoveClaimedPending(context.Context, messagequeue.QueueSessionIdentity, string, string, string) (*messagequeue.QueueRemovalResult, error)
}

func (h *Handlers) censusCaller(ctx context.Context, msg *ws.Message) (messagequeue.QueueSessionIdentity, string, claimedQueueService, *ws.Message) {
	principal, ok := mcpscope.PrincipalFromContext(ctx)
	caller, callerOK := authn.IdentityFromContext(ctx)
	if !validCensusPrincipal(principal, ok) || !callerOK || caller.UserID == "" || caller.UserID != principal.OriginalOwnerID {
		response, _ := ws.NewError(msg.ID, msg.Action, ws.ErrorCodeForbidden, "trusted current queue execution is required", nil)
		return messagequeue.QueueSessionIdentity{}, "", nil, response
	}
	if h.taskSvc == nil || h.messageQueue == nil {
		response, _ := ws.NewError(msg.ID, msg.Action, ws.ErrorCodeInternalError, "queue census is unavailable", nil)
		return messagequeue.QueueSessionIdentity{}, "", nil, response
	}
	task, err := h.taskSvc.GetTask(ctx, principal.CallerTaskID)
	if err != nil || task == nil || task.WorkspaceID != principal.WorkspaceID {
		response, _ := ws.NewError(msg.ID, msg.Action, ws.ErrorCodeForbidden, "task is outside the current queue scope", nil)
		return messagequeue.QueueSessionIdentity{}, "", nil, response
	}
	if err := h.taskSvc.AuthorizeTaskSessionIncarnationAccess(ctx, principal.CallerTaskID, principal.CallerSessionID, principal.OriginalQueueIncarnationID); err != nil {
		response, _ := ws.NewError(msg.ID, msg.Action, ws.ErrorCodeForbidden, "queue session is no longer current", nil)
		return messagequeue.QueueSessionIdentity{}, "", nil, response
	}
	queue, ok := h.messageQueue.(claimedQueueService)
	if !ok {
		response, _ := ws.NewError(msg.ID, msg.Action, ws.ErrorCodeInternalError, "queue census is unavailable", nil)
		return messagequeue.QueueSessionIdentity{}, "", nil, response
	}
	identity := messagequeue.QueueSessionIdentity{
		TaskID: principal.CallerTaskID, SessionID: principal.CallerSessionID,
		SessionIncarnationID: principal.OriginalQueueIncarnationID,
	}
	return identity, principal.ExecutionID, queue, nil
}

func validCensusPrincipal(principal mcpscope.Principal, present bool) bool {
	return present && principal.WorkspaceID != "" && principal.OriginalOwnerID != "" && principal.OriginalWorkspaceID == principal.WorkspaceID &&
		principal.ExecutionID != "" && principal.OriginalQueueIncarnationID != "" &&
		principal.OriginalQueueIncarnationID == principal.CallerIncarnationID &&
		(principal.Surface == mcpprofile.SurfaceKanbanTask || principal.Surface == mcpprofile.SurfaceOfficeTask)
}

func parseCensusPayload(payload json.RawMessage) bool {
	if len(payload) == 0 {
		return true
	}
	var fields map[string]json.RawMessage
	return json.Unmarshal(payload, &fields) == nil && fields != nil && len(fields) == 0
}

func parseClaimedRemovalPayload(payload json.RawMessage) (string, string, bool) {
	if len(payload) == 0 || len(payload) > 2048 {
		return "", "", false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(payload, &fields) != nil || len(fields) != 2 {
		return "", "", false
	}
	var entryID, claim string
	if json.Unmarshal(fields["entry_id"], &entryID) != nil || json.Unmarshal(fields["claim"], &claim) != nil ||
		entryID == "" || len(entryID) > 256 || !utf8.ValidString(entryID) || !validClaimHex(claim) {
		return "", "", false
	}
	return entryID, claim, true
}

func validClaimHex(claim string) bool {
	if len(claim) != 64 {
		return false
	}
	for _, ch := range claim {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
	}
	return true
}

func (h *Handlers) handleMessageQueueCensus(ctx context.Context, msg *ws.Message) (*ws.Message, error) {
	if !parseCensusPayload(msg.Payload) {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeBadRequest, "census requires an empty object", nil)
	}
	identity, executionID, queue, refusal := h.censusCaller(ctx, msg)
	if refusal != nil {
		return refusal, nil
	}
	principal, _ := mcpscope.PrincipalFromContext(ctx)
	census, err := queue.ObservePendingCensus(
		messagequeue.WithCensusOwnerFence(messagequeue.WithCensusWorkspaceFence(ctx, principal.OriginalWorkspaceID), principal.OriginalOwnerID), identity, executionID,
	)
	if err != nil {
		return censusQueueError(msg, err)
	}
	encoded, err := json.Marshal(census)
	if err != nil || len(encoded) > censusWireLimit {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeResourceExhausted, "queue census exceeds response limit", nil)
	}
	return ws.NewResponse(msg.ID, msg.Action, census)
}

func (h *Handlers) handleRemoveClaimedQueueEntry(ctx context.Context, msg *ws.Message) (*ws.Message, error) {
	entryID, claim, valid := parseClaimedRemovalPayload(msg.Payload)
	if !valid {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeBadRequest, "entry_id and claim are required", nil)
	}
	identity, executionID, queue, refusal := h.censusCaller(ctx, msg)
	if refusal != nil {
		return refusal, nil
	}
	principal, _ := mcpscope.PrincipalFromContext(ctx)
	result, err := queue.RemoveClaimedPending(
		messagequeue.WithCensusOwnerFence(messagequeue.WithCensusWorkspaceFence(ctx, principal.OriginalWorkspaceID), principal.OriginalOwnerID), identity, executionID, entryID, claim,
	)
	if err != nil {
		return censusQueueError(msg, err)
	}
	if result == nil || len(result.Removed) != 1 || result.Removed[0].ID != entryID {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeInternalError, "queue removal result is unavailable", nil)
	}
	h.releaseClaimedRemovalAttachments(ctx, identity, result)
	// The effect has committed; the response deliberately excludes the removed
	// body and attachments.
	return ws.NewResponse(msg.ID, msg.Action, map[string]any{"entry_id": entryID, "removed": true})
}

func (h *Handlers) releaseClaimedRemovalAttachments(ctx context.Context, identity messagequeue.QueueSessionIdentity, removal *messagequeue.QueueRemovalResult) {
	candidates := claimedRemovalAttachmentCandidates(removal)
	if len(candidates) == 0 {
		return
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := h.taskSvc.ReleaseMessageAttachments(cleanupCtx, identity.TaskID, identity.SessionID, candidates); err != nil {
		h.logger.Warn("failed to release claimed queue attachments", zap.Error(err))
	}
}

func claimedRemovalAttachmentCandidates(removal *messagequeue.QueueRemovalResult) []v1.MessageAttachment {
	if removal == nil {
		return nil
	}
	retained := make(map[string]struct{})
	for _, entry := range removal.Retained {
		for _, attachment := range entry.Attachments {
			retained[attachment.AttachmentID] = struct{}{}
		}
	}
	seen := make(map[string]struct{})
	var candidates []v1.MessageAttachment
	for _, entry := range removal.Removed {
		for _, attachment := range entry.Attachments {
			if attachment.AttachmentID == "" {
				continue
			}
			if _, exists := retained[attachment.AttachmentID]; exists {
				continue
			}
			if _, exists := seen[attachment.AttachmentID]; exists {
				continue
			}
			seen[attachment.AttachmentID] = struct{}{}
			candidates = append(candidates, v1.MessageAttachment{AttachmentID: attachment.AttachmentID})
		}
	}
	return candidates
}

func censusQueueError(msg *ws.Message, err error) (*ws.Message, error) {
	code := ws.ErrorCodeConflict
	if errors.Is(err, messagequeue.ErrSessionIdentityMismatch) || errors.Is(err, messagequeue.ErrQueueAdmissionUnavailable) {
		code = ws.ErrorCodeForbidden
	} else if errors.Is(err, messagequeue.ErrCensusResourceExhausted) {
		code = ws.ErrorCodeResourceExhausted
	}
	return ws.NewError(msg.ID, msg.Action, code, "queue census or claim is no longer current", nil)
}
