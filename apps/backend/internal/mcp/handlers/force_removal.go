package handlers

import (
	"context"
	"encoding/json"
	"github.com/kandev/kandev/internal/task/service"
	ws "github.com/kandev/kandev/pkg/websocket"
)

type forceRemovalCommitRequest struct {
	TaskID                 string `json:"task_id"`
	WorkspaceID            string `json:"workspace_id"`
	ExpectedTaskGeneration string `json:"expected_task_generation"`
	AdmissionGeneration    string `json:"admission_generation"`
	OperationID            string `json:"operation_id"`
	RequestDigest          string `json:"request_digest"`
	PreviewDigest          string `json:"preview_digest"`
	GrantID                string `json:"grant_id"`
}

func (h *Handlers) handleCommitForceRemoval(ctx context.Context, msg *ws.Message) (*ws.Message, error) {
	if h.taskSvc == nil || h.liveCallerVerifier == nil {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeInternalError, "force removal admission is not configured", nil)
	}
	var req forceRemovalCommitRequest
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeBadRequest, "invalid payload", nil)
	}
	caller, err := h.liveCallerVerifier.VerifyLiveCaller(ctx)
	if err != nil {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeForbidden, "live caller is required", nil)
	}
	claim, replay, err := h.taskSvc.AdmitForceRemovalWithGrant(ctx, service.ForceRemovalAdmissionRequest{TaskID: req.TaskID, WorkspaceID: req.WorkspaceID, ExpectedTaskGeneration: req.ExpectedTaskGeneration, AdmissionGeneration: req.AdmissionGeneration, OperationID: req.OperationID, RequestDigest: req.RequestDigest, PreviewDigest: req.PreviewDigest}, req.GrantID, service.ForceRemovalTrustedCaller{TaskID: caller.CallerTaskID, SessionID: caller.CallerSessionID})
	if err != nil {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeForbidden, "force removal admission denied", nil)
	}
	return ws.NewResponse(msg.ID, msg.Action, map[string]interface{}{"task_id": claim.TaskID, "operation_id": claim.OperationID, "replay": replay})
}
