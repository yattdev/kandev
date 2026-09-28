package plugins

import (
	"context"

	"github.com/kandev/kandev/pkg/pluginsdk"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const exactWorkspacePageLimit int32 = 1

func (h *pluginHost) ListWorkspacesExact(ctx context.Context, query pluginsdk.ExactWorkspaceQuery) ([]pluginsdk.Workspace, *pluginsdk.ExactPageInfo, error) {
	if query.Page.Limit < 0 || query.Page.Limit > exactWorkspacePageLimit {
		return nil, nil, status.Error(codes.InvalidArgument, "exact workspace page limit is invalid")
	}
	receipt, err := h.authorizeExactReadReceipt(query.WorkspaceID, query.CapabilityRevision, "host.v2.read:workspaces", CanonicalApprovalDigest("workspaces", query.WorkspaceID, query.Page.SnapshotVersion))
	if err != nil {
		return nil, nil, err
	}
	if h.taskData == nil || h.exactSnapshots == nil {
		return nil, nil, resourceExhausted("exact workspace read is unavailable")
	}
	workspaces, err := h.taskData.ListWorkspaces(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, workspace := range workspaces {
		if workspace == nil {
			return nil, nil, status.Error(codes.FailedPrecondition, "exact workspace projection is incomplete")
		}
		if workspace.ID != query.WorkspaceID {
			continue
		}
		item := workspaceModelToDTO(workspace)
		version, err := exactProjectionDigest(item)
		if err != nil {
			return nil, nil, status.Error(codes.FailedPrecondition, "exact workspace projection is incomplete")
		}
		binding := exactSnapshotBinding{InstallationID: h.installationID, WorkspaceID: query.WorkspaceID, FilterDigest: "workspaces", ApprovalRevision: query.CapabilityRevision, ProjectionVersion: version}
		if err := h.validateExactWorkspacePage(binding, query.Page); err != nil {
			return nil, nil, err
		}
		return []pluginsdk.Workspace{item}, &pluginsdk.ExactPageInfo{SnapshotVersion: version, AuditID: receipt.AuditID}, nil
	}
	return nil, nil, status.Error(codes.NotFound, "workspace not found")
}

func (h *pluginHost) validateExactWorkspacePage(binding exactSnapshotBinding, page pluginsdk.ExactPage) error {
	if page.SnapshotVersion != "" && page.SnapshotVersion != binding.ProjectionVersion {
		return status.Error(codes.InvalidArgument, "exact snapshot version is invalid")
	}
	if page.Cursor != "" {
		offset, err := h.exactSnapshots.offset(page.Cursor, binding)
		if err != nil || offset != 0 {
			return status.Error(codes.InvalidArgument, "exact snapshot cursor is invalid")
		}
	}
	return nil
}
