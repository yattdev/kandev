package plugins

import (
	"context"

	"github.com/kandev/kandev/pkg/pluginsdk"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// exactApprovalReader reads current approval rows at request time so a
// context response cannot outlive a revocation or manifest review.
type exactApprovalReader func(string) ([]CapabilityApproval, error)
type exactReadAuthorizer func(string, uint64, string, string) ApprovalDecision
type exactReadReceiptRecorder func(ApprovalReceipt) error

func (h *pluginHost) authorizeExactRead(workspaceID string, revision uint64, capabilityID, requestDigest string) error {
	_, err := h.authorizeExactReadReceipt(workspaceID, revision, capabilityID, requestDigest)
	return err
}

func (h *pluginHost) authorizeExactReadReceipt(workspaceID string, revision uint64, capabilityID, requestDigest string) (ApprovalReceipt, error) {
	receipt, err := h.authorizeExactReadDecision(workspaceID, revision, capabilityID, requestDigest)
	if err != nil {
		return ApprovalReceipt{}, err
	}
	if h.exactReadReceipt == nil {
		return ApprovalReceipt{}, status.Error(codes.FailedPrecondition, "exact read receipt is unavailable")
	}
	if err := h.exactReadReceipt(receipt); err != nil {
		return ApprovalReceipt{}, status.Error(codes.Unavailable, "exact read receipt is unavailable")
	}
	return receipt, nil
}

func (h *pluginHost) authorizeExactReadDecision(workspaceID string, revision uint64, capabilityID, requestDigest string) (ApprovalReceipt, error) {
	if h.exactAuthorize == nil || workspaceID == "" || revision == 0 {
		return ApprovalReceipt{}, status.Error(codes.PermissionDenied, "exact read capability is denied")
	}
	decision := h.exactAuthorize(workspaceID, revision, capabilityID, requestDigest)
	if !decision.Allowed {
		return ApprovalReceipt{}, status.Error(codes.PermissionDenied, "exact read capability is denied")
	}
	return decision.Receipt, nil
}

// GetCapabilityContext returns only Host-derived connection identity and
// ledger state. Callers must still present the current approval revision to
// every exact operation; this response is never an authorization grant.
func (h *pluginHost) GetCapabilityContext(_ context.Context) (*pluginsdk.CapabilityContext, error) {
	if h.installationID == "" || h.manifestDigest == "" || h.exactApprovals == nil {
		return nil, status.Error(codes.FailedPrecondition, "exact capability context is unavailable")
	}
	approvals, err := h.exactApprovals(h.installationID)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "exact capability context is unavailable: %v", err)
	}
	context := &pluginsdk.CapabilityContext{
		ContractVersion: pluginsdk.ExactHostContractVersion,
		InstallationID:  h.installationID,
		ManifestDigest:  h.manifestDigest,
		Approvals:       make([]pluginsdk.CapabilityApprovalContext, 0, len(approvals)),
	}
	for _, approval := range approvals {
		if approval.InstallationID != h.installationID || approval.WorkspaceID == "" || approval.Revision == 0 {
			return nil, status.Error(codes.FailedPrecondition, "exact capability context is unavailable")
		}
		if approval.State != ApprovalStateActive && approval.State != ApprovalStateRevoked {
			return nil, status.Error(codes.FailedPrecondition, "exact capability context is unavailable")
		}
		context.Approvals = append(context.Approvals, pluginsdk.CapabilityApprovalContext{
			ApprovalID:  CanonicalApprovalDigest("capability-approval", approval.InstallationID, approval.WorkspaceID),
			WorkspaceID: approval.WorkspaceID,
			Revision:    approval.Revision,
			Status:      pluginsdk.CapabilityApprovalStatus(approval.State),
		})
	}
	return context, nil
}

var _ pluginsdk.ExactHost = (*pluginHost)(nil)
var _ pluginsdk.ExactWorkspaceHost = (*pluginHost)(nil)
