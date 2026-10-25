package plugins

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/kandev/kandev/internal/orchestrator/messagequeue"
	tasksqlite "github.com/kandev/kandev/internal/task/repository/sqlite"
	"github.com/kandev/kandev/pkg/pluginsdk"
)

const exactTaskDescriptionCommandCapability = "host.v2.write:tasks"

// exactTaskCommandGrantRequest is intentionally Host-private. Installation identity, capability, ID,
// expiry, receipt, and action digest all come from the Host rather than this
// request.
type exactTaskCommandGrantRequest struct {
	WorkspaceID, TaskID, DecisionEvidenceSnapshotVersion string
	CapabilityRevision                                   uint64
	PendingTransition                                    pluginsdk.ExactPendingTaskTransition
	Marker, IdempotencyKey                               string
}

func (h *pluginHost) issueExactTaskCommandGrant(ctx context.Context, request exactTaskCommandGrantRequest) (tasksqlite.ExactTaskCommandGrant, error) {
	grant, binding, pending, issuer, err := h.exactTaskCommandGrant(ctx, request)
	if err != nil {
		return tasksqlite.ExactTaskCommandGrant{}, ErrExactTaskGrantUnavailable
	}
	if err := issuer.Issue(ctx, grant, binding.ProjectionVersion, pending); err != nil {
		return tasksqlite.ExactTaskCommandGrant{}, ErrExactTaskGrantUnavailable
	}
	return grant, nil
}

// executeExactTaskCommand keeps the future public writer behind a private
// Host boundary until its wire contract is independently reviewed.
func (h *pluginHost) executeExactTaskCommand(ctx context.Context, request exactTaskCommandGrantRequest, expectedResourceVersion int64) (tasksqlite.ExactTaskDescriptionReceipt, error) {
	grant, binding, pending, issuer, err := h.exactTaskCommandGrant(ctx, request)
	if err != nil || expectedResourceVersion <= 0 {
		return tasksqlite.ExactTaskDescriptionReceipt{}, ErrExactTaskGrantUnavailable
	}
	identityRequest := pluginsdk.ExactTaskUpdateRequest{
		WorkspaceID: request.WorkspaceID, TaskID: request.TaskID,
		CapabilityRevision: request.CapabilityRevision, DecisionEvidenceSnapshotVersion: request.DecisionEvidenceSnapshotVersion,
		PendingTransition: request.PendingTransition, Marker: request.Marker,
		IdempotencyKey: request.IdempotencyKey, ExpectedResourceVersion: expectedResourceVersion,
	}
	receipt, err := issuer.Execute(ctx, grant, binding.ProjectionVersion, pending, request.Marker, expectedResourceVersion, exactTaskUpdateRequestIdentity(identityRequest))
	if err != nil {
		return tasksqlite.ExactTaskDescriptionReceipt{}, ErrExactTaskGrantUnavailable
	}
	return receipt, nil
}

func (h *pluginHost) UpdateTaskExact(ctx context.Context, request pluginsdk.ExactTaskUpdateRequest) (*pluginsdk.ExactTaskUpdateReceipt, error) {
	if h == nil || h.installationID == "" || request.ExpectedResourceVersion <= 0 || !validExactTaskCommandGrantRequest(exactTaskCommandGrantRequest{WorkspaceID: request.WorkspaceID, TaskID: request.TaskID, CapabilityRevision: request.CapabilityRevision, Marker: request.Marker, IdempotencyKey: request.IdempotencyKey}) {
		return nil, ErrExactTaskGrantUnavailable
	}
	if _, err := h.authorizeExactReadDecision(request.WorkspaceID, request.CapabilityRevision, exactTaskDescriptionCommandCapability, CanonicalApprovalDigest("exact-task-command-replay", request.TaskID, request.IdempotencyKey)); err != nil {
		return nil, ErrExactTaskGrantUnavailable
	}
	if h.exactTaskCommandGrantIssuerDep == nil {
		return nil, ErrExactTaskGrantUnavailable
	}
	issuer := h.exactTaskCommandGrantIssuerDep()
	if issuer == nil {
		return nil, ErrExactTaskGrantUnavailable
	}
	replayed, err := issuer.Replay(ctx, h.installationID, request.WorkspaceID, request.TaskID, request.IdempotencyKey, exactTaskUpdateRequestIdentity(request), request.CapabilityRevision)
	if err != nil {
		return nil, ErrExactTaskGrantUnavailable
	}
	if replayed != nil {
		return exactTaskUpdateReceipt(*replayed), nil
	}
	receipt, err := h.executeExactTaskCommand(ctx, exactTaskCommandGrantRequest{WorkspaceID: request.WorkspaceID, TaskID: request.TaskID, CapabilityRevision: request.CapabilityRevision, DecisionEvidenceSnapshotVersion: request.DecisionEvidenceSnapshotVersion, PendingTransition: request.PendingTransition, Marker: request.Marker, IdempotencyKey: request.IdempotencyKey}, request.ExpectedResourceVersion)
	if err != nil {
		return nil, ErrExactTaskGrantUnavailable
	}
	return exactTaskUpdateReceipt(receipt), nil
}

func exactTaskUpdateRequestIdentity(request pluginsdk.ExactTaskUpdateRequest) string {
	encoded, _ := json.Marshal(request)
	return CanonicalApprovalDigest("exact-task-update-request", string(encoded))
}

func exactTaskUpdateReceipt(receipt tasksqlite.ExactTaskDescriptionReceipt) *pluginsdk.ExactTaskUpdateReceipt {
	outcome := pluginsdk.ExactTaskUpdateDurable
	if receipt.Pending {
		outcome = pluginsdk.ExactTaskUpdatePending
	}
	return &pluginsdk.ExactTaskUpdateReceipt{AuditID: receipt.AuditID, ResourceVersion: receipt.ResourceVersion, Outcome: outcome}
}

func (h *pluginHost) exactTaskCommandGrant(ctx context.Context, request exactTaskCommandGrantRequest) (tasksqlite.ExactTaskCommandGrant, exactSnapshotBinding, messagequeue.ExactPendingTransition, ExactTaskCommandGrantIssuer, error) {
	if h == nil || h.installationID == "" || !validExactTaskCommandGrantRequest(request) {
		return tasksqlite.ExactTaskCommandGrant{}, exactSnapshotBinding{}, messagequeue.ExactPendingTransition{}, nil, ErrExactTaskGrantUnavailable
	}
	binding, pending, err := h.exactTaskCommandGrantEvidence(request)
	if err != nil {
		return tasksqlite.ExactTaskCommandGrant{}, exactSnapshotBinding{}, messagequeue.ExactPendingTransition{}, nil, ErrExactTaskGrantUnavailable
	}
	receipt, err := h.authorizeExactReadReceipt(request.WorkspaceID, request.CapabilityRevision, exactTaskDescriptionCommandCapability, CanonicalApprovalDigest("exact-task-command-grant", request.TaskID, binding.ProjectionVersion, pending.SessionID, request.Marker, request.IdempotencyKey))
	if err != nil {
		return tasksqlite.ExactTaskCommandGrant{}, exactSnapshotBinding{}, messagequeue.ExactPendingTransition{}, nil, ErrExactTaskGrantUnavailable
	}
	issuerDep := h.exactTaskCommandGrantIssuerDep
	if issuerDep == nil {
		return tasksqlite.ExactTaskCommandGrant{}, exactSnapshotBinding{}, messagequeue.ExactPendingTransition{}, nil, ErrExactTaskGrantUnavailable
	}
	issuer := issuerDep()
	if issuer == nil {
		return tasksqlite.ExactTaskCommandGrant{}, exactSnapshotBinding{}, messagequeue.ExactPendingTransition{}, nil, ErrExactTaskGrantUnavailable
	}
	now := time.Now().UTC()
	grant := tasksqlite.ExactTaskCommandGrant{
		ID:               uuid.NewString(),
		InstallationID:   h.installationID,
		WorkspaceID:      request.WorkspaceID,
		TaskID:           request.TaskID,
		CapabilityID:     exactTaskDescriptionCommandCapability,
		ReceiptAuditID:   receipt.AuditID,
		ApprovalRevision: request.CapabilityRevision,
		ActionDigest:     CanonicalApprovalDigest("exact-task-description-marker", request.Marker),
		IdempotencyKey:   request.IdempotencyKey,
		ExpiresAt:        now.Add(exactTaskGrantTTL),
	}
	return grant, binding, pending, issuer, nil
}

func validExactTaskCommandGrantRequest(request exactTaskCommandGrantRequest) bool {
	return isBoundedApprovalIdentifier(request.WorkspaceID) &&
		isBoundedApprovalIdentifier(request.TaskID) &&
		request.CapabilityRevision > 0 &&
		isBoundedAuthorizationData(request.Marker) &&
		isBoundedAuthorizationData(request.IdempotencyKey)
}

func (h *pluginHost) exactTaskCommandGrantEvidence(request exactTaskCommandGrantRequest) (exactSnapshotBinding, messagequeue.ExactPendingTransition, error) {
	binding, err := h.exactDecisionEvidenceSnapshotBinding(request.WorkspaceID, request.CapabilityRevision, request.DecisionEvidenceSnapshotVersion)
	if err != nil || binding.ProjectionVersion == "" {
		return exactSnapshotBinding{}, messagequeue.ExactPendingTransition{}, ErrExactTaskGrantUnavailable
	}
	pending, err := exactPendingTransitionFromSDK(request.PendingTransition)
	if err != nil || pending.WorkspaceID != request.WorkspaceID || pending.TaskID != request.TaskID {
		return exactSnapshotBinding{}, messagequeue.ExactPendingTransition{}, ErrExactTaskGrantUnavailable
	}
	return binding, pending, nil
}

func exactPendingTransitionFromSDK(in pluginsdk.ExactPendingTaskTransition) (messagequeue.ExactPendingTransition, error) {
	queuedAt, err := time.Parse(time.RFC3339Nano, in.QueuedAt)
	if err != nil || queuedAt.IsZero() {
		return messagequeue.ExactPendingTransition{}, ErrExactTaskGrantUnavailable
	}
	return messagequeue.ExactPendingTransition{SessionID: in.SessionID, TaskID: in.TaskID, WorkspaceID: in.WorkspaceID, SessionIncarnationID: in.SessionIncarnationID, WorkflowID: in.WorkflowID, WorkflowStepID: in.WorkflowStepID, Position: int(in.StepPosition), QueuedAt: queuedAt.UTC(), ResourceVersion: in.ResourceVersion, TaskResourceVersion: in.TaskResourceVersion, SessionResourceVersion: in.SessionResourceVersion, QueueGeneration: in.QueueGeneration}, nil
}
