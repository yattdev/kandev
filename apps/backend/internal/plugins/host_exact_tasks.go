package plugins

import (
	"context"
	"errors"

	taskmodels "github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository/repoerrors"
	"github.com/kandev/kandev/pkg/pluginsdk"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const exactTaskPageLimit int32 = 99

// ListTasksExact uses the durable repository snapshot as its version. The
// signed cursor binds that opaque version to this installation, workspace,
// approval revision, and task filter; a token therefore cannot cross either
// a Host connection or a workspace boundary.
func (h *pluginHost) ListTasksExact(ctx context.Context, query pluginsdk.ExactTaskQuery) ([]pluginsdk.ExactTask, *pluginsdk.ExactPageInfo, error) {
	receipt, err := h.authorizeExactReadReceipt(query.WorkspaceID, query.CapabilityRevision, "host.v2.read:tasks", CanonicalApprovalDigest("tasks", query.WorkspaceID, query.Page.SnapshotVersion))
	if err != nil {
		return nil, nil, err
	}
	if h.taskData == nil || h.exactSnapshots == nil {
		return nil, nil, resourceExhausted("exact task read is unavailable")
	}
	binding, err := h.exactTaskSnapshot(ctx, query)
	if err != nil {
		return nil, nil, err
	}
	offset, err := h.exactTaskOffset(binding, query.Page)
	if err != nil {
		return nil, nil, err
	}
	limit := exactTaskPageLimit
	if query.Page.Limit > 0 {
		limit = query.Page.Limit
	}
	rows, err := h.taskData.PageExactTaskSnapshot(ctx, binding.ProjectionVersion, offset, int(limit)+1)
	if err != nil {
		return nil, nil, exactTaskSnapshotError(err)
	}
	items := make([]pluginsdk.ExactTask, min(len(rows), int(limit)))
	for i := range items {
		if rows[i].WorkspaceID != query.WorkspaceID {
			return nil, nil, status.Error(codes.FailedPrecondition, "exact task projection is incomplete")
		}
		items[i] = exactTaskToDTO(rows[i])
	}
	snapshotVersion, err := h.exactSnapshots.create(binding, 0)
	if err != nil {
		return nil, nil, status.Error(codes.Unavailable, "exact task snapshot is unavailable")
	}
	info := &pluginsdk.ExactPageInfo{SnapshotVersion: snapshotVersion, HasMore: len(rows) > len(items), AuditID: receipt.AuditID}
	if info.HasMore {
		cursor, cursorErr := h.exactSnapshots.create(binding, offset+len(items))
		if cursorErr != nil {
			return nil, nil, status.Error(codes.Unavailable, "exact task cursor is unavailable")
		}
		info.NextCursor = cursor
	}
	return items, info, nil
}

func (h *pluginHost) GetTaskExact(ctx context.Context, query pluginsdk.ExactTaskGetQuery) (*pluginsdk.ExactTask, error) {
	if err := h.authorizeExactRead(query.WorkspaceID, query.CapabilityRevision, "host.v2.read:tasks", CanonicalApprovalDigest("task", query.WorkspaceID, query.TaskID, query.SnapshotVersion)); err != nil {
		return nil, err
	}
	if h.taskData == nil || query.TaskID == "" || query.SnapshotVersion == "" {
		return nil, status.Error(codes.FailedPrecondition, "exact task read is unavailable")
	}
	binding, err := h.exactTaskBinding(query.WorkspaceID, query.CapabilityRevision, query.SnapshotVersion)
	if err != nil {
		return nil, err
	}
	row, err := h.taskData.GetExactTaskSnapshotTask(ctx, binding.ProjectionVersion, query.TaskID)
	if err != nil {
		return nil, exactTaskSnapshotError(err)
	}
	if row == nil || row.WorkspaceID != query.WorkspaceID {
		return nil, status.Error(codes.FailedPrecondition, "exact task projection is incomplete")
	}
	result := exactTaskToDTO(*row)
	return &result, nil
}

func (h *pluginHost) exactTaskSnapshot(ctx context.Context, query pluginsdk.ExactTaskQuery) (exactSnapshotBinding, error) {
	if query.Page.Limit < 0 || query.Page.Limit > exactTaskPageLimit || query.Page.Cursor != "" && query.Page.SnapshotVersion == "" {
		return exactSnapshotBinding{}, status.Error(codes.InvalidArgument, "exact snapshot page is invalid")
	}
	if query.Page.SnapshotVersion != "" {
		return h.exactTaskBinding(query.WorkspaceID, query.CapabilityRevision, query.Page.SnapshotVersion)
	}
	snapshot, err := h.taskData.OpenExactTaskSnapshot(ctx, taskmodels.ExactTaskSnapshotRequest{WorkspaceID: query.WorkspaceID})
	if err != nil {
		return exactSnapshotBinding{}, exactTaskSnapshotError(err)
	}
	if snapshot == nil || snapshot.Token == "" || snapshot.WorkspaceID != query.WorkspaceID {
		return exactSnapshotBinding{}, status.Error(codes.FailedPrecondition, "exact task snapshot is unavailable")
	}
	return h.exactPageBinding(query.WorkspaceID, query.CapabilityRevision, "tasks", snapshot.Token), nil
}

func (h *pluginHost) exactTaskBinding(workspaceID string, revision uint64, snapshotVersion string) (exactSnapshotBinding, error) {
	if h.exactSnapshots == nil || snapshotVersion == "" {
		return exactSnapshotBinding{}, status.Error(codes.FailedPrecondition, "exact task snapshot is unavailable")
	}
	cursor, err := h.exactSnapshots.parse(snapshotVersion)
	want := h.exactPageBinding(workspaceID, revision, "tasks", cursor.ProjectionVersion)
	if err != nil || cursor.Offset != 0 || cursor.exactSnapshotBinding != want {
		return exactSnapshotBinding{}, status.Error(codes.InvalidArgument, "exact task snapshot is invalid")
	}
	return cursor.exactSnapshotBinding, nil
}

func (h *pluginHost) exactTaskOffset(binding exactSnapshotBinding, page pluginsdk.ExactPage) (int, error) {
	if page.Limit < 0 || page.Limit > exactTaskPageLimit {
		return 0, status.Error(codes.InvalidArgument, "exact snapshot page is invalid")
	}
	if page.Cursor == "" {
		return 0, nil
	}
	offset, err := h.exactSnapshots.offset(page.Cursor, binding)
	if err != nil {
		return 0, status.Error(codes.InvalidArgument, "exact snapshot cursor is invalid")
	}
	return offset, nil
}

func exactTaskToDTO(row taskmodels.ExactTaskSnapshotTask) pluginsdk.ExactTask {
	return pluginsdk.ExactTask{ID: row.ID, WorkspaceID: row.WorkspaceID, WorkflowID: row.WorkflowID, WorkflowStepID: row.WorkflowStepID, Title: row.Title, Description: row.Description, State: string(row.State), Priority: row.Priority, Position: int32(row.Position), Archived: row.Archived, ResourceVersion: row.ResourceVersion}
}

func exactTaskSnapshotError(err error) error {
	if errors.Is(err, repoerrors.ErrExactTaskSnapshotUnavailable) {
		return status.Error(codes.FailedPrecondition, "exact task snapshot is unavailable")
	}
	if errors.Is(err, repoerrors.ErrTaskNotFound) {
		return status.Error(codes.NotFound, "task not found")
	}
	return err
}

var _ pluginsdk.ExactTaskHost = (*pluginHost)(nil)
