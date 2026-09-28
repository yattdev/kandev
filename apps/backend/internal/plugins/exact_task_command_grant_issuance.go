package plugins

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/kandev/kandev/internal/orchestrator/messagequeue"
	tasksqlite "github.com/kandev/kandev/internal/task/repository/sqlite"
	"github.com/kandev/kandev/pkg/pluginsdk"
)

const exactTaskDescriptionCommandCapability = "host.v2.write:tasks"

// exactTaskCommandGrantRequest is intentionally Host-private while the public
// UpdateTaskExact RPC remains disabled. Installation identity, capability, ID,
// expiry, receipt, and action digest all come from the Host rather than this
// request.
type exactTaskCommandGrantRequest struct {
	WorkspaceID, TaskID, DecisionEvidenceSnapshotVersion string
	CapabilityRevision                                   uint64
	PendingTransition                                    pluginsdk.ExactPendingTaskTransition
	Marker, IdempotencyKey                               string
}

func (h *pluginHost) issueExactTaskCommandGrant(ctx context.Context, request exactTaskCommandGrantRequest) (tasksqlite.ExactTaskCommandGrant, error) {
	if h == nil || h.installationID == "" || !validExactTaskCommandGrantRequest(request) {
		return tasksqlite.ExactTaskCommandGrant{}, ErrExactTaskGrantUnavailable
	}
	binding, pending, err := h.exactTaskCommandGrantEvidence(request)
	if err != nil {
		return tasksqlite.ExactTaskCommandGrant{}, ErrExactTaskGrantUnavailable
	}
	receipt, err := h.authorizeExactReadReceipt(request.WorkspaceID, request.CapabilityRevision, exactTaskDescriptionCommandCapability, CanonicalApprovalDigest("exact-task-command-grant", request.TaskID, binding.ProjectionVersion, pending.SessionID, request.Marker, request.IdempotencyKey))
	if err != nil {
		return tasksqlite.ExactTaskCommandGrant{}, ErrExactTaskGrantUnavailable
	}
	issuerDep := h.exactTaskCommandGrantIssuerDep
	if issuerDep == nil {
		return tasksqlite.ExactTaskCommandGrant{}, ErrExactTaskGrantUnavailable
	}
	issuer := issuerDep()
	if issuer == nil {
		return tasksqlite.ExactTaskCommandGrant{}, ErrExactTaskGrantUnavailable
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
	if err := issuer.Issue(ctx, grant, binding.ProjectionVersion, pending); err != nil {
		return tasksqlite.ExactTaskCommandGrant{}, ErrExactTaskGrantUnavailable
	}
	return grant, nil
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
