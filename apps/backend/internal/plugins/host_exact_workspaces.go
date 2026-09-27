package plugins

import (
	"context"
	"time"

	"github.com/kandev/kandev/pkg/pluginsdk"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const exactWorkspacePageLimit int32 = 1

func (h *pluginHost) ListWorkspacesExact(ctx context.Context, query pluginsdk.ExactWorkspaceQuery) ([]pluginsdk.Workspace, *pluginsdk.ExactPageInfo, error) {
	if query.Page.Limit < 0 || query.Page.Limit > exactWorkspacePageLimit {
		return nil, nil, status.Error(codes.InvalidArgument, "exact workspace page limit is invalid")
	}
	if err := h.authorizeExactRead(query.WorkspaceID, query.CapabilityRevision, "host.v2.read:workspaces", CanonicalApprovalDigest("workspaces", query.WorkspaceID, query.Page.SnapshotVersion)); err != nil {
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
		if workspace.ID != query.WorkspaceID {
			continue
		}
		version := CanonicalApprovalDigest(workspace.ID, workspace.UpdatedAt.UTC().Format(time.RFC3339Nano))
		binding := exactSnapshotBinding{InstallationID: h.installationID, WorkspaceID: query.WorkspaceID, FilterDigest: "workspaces", ApprovalRevision: query.CapabilityRevision, ProjectionVersion: version}
		if query.Page.SnapshotVersion != "" && query.Page.SnapshotVersion != version {
			return nil, nil, status.Error(codes.InvalidArgument, "exact snapshot version is invalid")
		}
		if query.Page.Cursor != "" {
			offset, err := h.exactSnapshots.offset(query.Page.Cursor, binding)
			if err != nil || offset != 0 {
				return nil, nil, status.Error(codes.InvalidArgument, "exact snapshot cursor is invalid")
			}
		}
		return []pluginsdk.Workspace{workspaceModelToDTO(workspace)}, &pluginsdk.ExactPageInfo{SnapshotVersion: version}, nil
	}
	return nil, nil, status.Error(codes.NotFound, "workspace not found")
}
