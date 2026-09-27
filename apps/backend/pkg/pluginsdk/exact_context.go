package pluginsdk

import (
	"context"
	"fmt"

	pluginv1 "github.com/kandev/kandev/proto/kandev/plugin/v1"
)

// ExactHostContractVersion identifies the additive exact Host contract. It
// does not change authority granted by any v1 Host method.
const ExactHostContractVersion = "2"

type CapabilityApprovalStatus string

const (
	CapabilityApprovalStatusActive  CapabilityApprovalStatus = "active"
	CapabilityApprovalStatusRevoked CapabilityApprovalStatus = "revoked"
)

// CapabilityApprovalContext is the current approval row visible on the
// connection-bound plugin installation. It is informational only and cannot
// be replayed as authority for an exact read or command.
type CapabilityApprovalContext struct {
	ApprovalID  string
	WorkspaceID string
	Revision    uint64
	Status      CapabilityApprovalStatus
}

// CapabilityContext is the Host-derived identity and approval state for one
// plugin broker connection.
type CapabilityContext struct {
	ContractVersion string
	InstallationID  string
	ManifestDigest  string
	Approvals       []CapabilityApprovalContext
}

// ExactHost is an optional Host extension. Keeping it separate preserves
// source compatibility for every existing v1 Host implementation.
type ExactHost interface {
	GetCapabilityContext(context.Context) (*CapabilityContext, error)
}

// Exact returns the optional exact Host extension when the connected Host
// supports it.
func Exact(host Host) (ExactHost, bool) {
	exact, ok := host.(ExactHost)
	return exact, ok
}

func capabilityContextToProto(in *CapabilityContext) *pluginv1.GetCapabilityContextResponse {
	if in == nil {
		return &pluginv1.GetCapabilityContextResponse{}
	}
	approvals := make([]*pluginv1.CapabilityApprovalContext, 0, len(in.Approvals))
	for _, approval := range in.Approvals {
		approvals = append(approvals, &pluginv1.CapabilityApprovalContext{
			ApprovalId: approval.ApprovalID, WorkspaceId: approval.WorkspaceID,
			Revision: approval.Revision, Status: string(approval.Status),
		})
	}
	return &pluginv1.GetCapabilityContextResponse{
		ContractVersion: in.ContractVersion, InstallationId: in.InstallationID,
		ManifestDigest: in.ManifestDigest, Approvals: approvals,
	}
}

func capabilityContextFromProto(in *pluginv1.GetCapabilityContextResponse) (*CapabilityContext, error) {
	if in == nil {
		return nil, fmt.Errorf("pluginsdk: capability context response is missing")
	}
	out := &CapabilityContext{
		ContractVersion: in.GetContractVersion(), InstallationID: in.GetInstallationId(),
		ManifestDigest: in.GetManifestDigest(), Approvals: make([]CapabilityApprovalContext, 0, len(in.GetApprovals())),
	}
	if out.ContractVersion != ExactHostContractVersion || out.InstallationID == "" || out.ManifestDigest == "" {
		return nil, fmt.Errorf("pluginsdk: capability context is incomplete")
	}
	for _, approval := range in.GetApprovals() {
		status := CapabilityApprovalStatus(approval.GetStatus())
		if approval.GetApprovalId() == "" || approval.GetWorkspaceId() == "" || approval.GetRevision() == 0 ||
			(status != CapabilityApprovalStatusActive && status != CapabilityApprovalStatusRevoked) {
			return nil, fmt.Errorf("pluginsdk: capability approval context is malformed")
		}
		out.Approvals = append(out.Approvals, CapabilityApprovalContext{
			ApprovalID: approval.GetApprovalId(), WorkspaceID: approval.GetWorkspaceId(),
			Revision: approval.GetRevision(), Status: status,
		})
	}
	return out, nil
}
