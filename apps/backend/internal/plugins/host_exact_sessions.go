package plugins

import (
	"context"
	"errors"
	"strconv"
	"time"

	taskmodels "github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository/repoerrors"
	"github.com/kandev/kandev/pkg/pluginsdk"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const exactSessionPageLimit int32 = 99

// ListSessionsExact returns the bounded lifecycle projection pinned by the
// task repository's durable workspace snapshot.
func (h *pluginHost) ListSessionsExact(ctx context.Context, query pluginsdk.ExactSessionQuery) ([]pluginsdk.ExactSession, *pluginsdk.ExactPageInfo, error) {
	if _, err := h.authorizeExactReadDecision(query.WorkspaceID, query.CapabilityRevision, "host.v2.read:tasks", CanonicalApprovalDigest("sessions", query.WorkspaceID, query.Page.SnapshotVersion)); err != nil {
		return nil, nil, err
	}
	if h.taskData == nil || h.exactSnapshots == nil {
		return nil, nil, resourceExhausted("exact session read is unavailable")
	}
	binding, err := h.exactSessionSnapshot(ctx, query)
	if err != nil {
		return nil, nil, err
	}
	offset, err := h.exactSessionOffset(binding, query.Page)
	if err != nil {
		return nil, nil, err
	}
	limit := exactSessionPageLimit
	if query.Page.Limit > 0 {
		limit = query.Page.Limit
	}
	rows, err := h.taskData.PageExactSessionSnapshot(ctx, binding.ProjectionVersion, offset, int(limit)+1)
	if err != nil {
		return nil, nil, exactSessionSnapshotError(err)
	}
	items, err := exactSessionDTOs(rows, query.WorkspaceID, limit)
	if err != nil {
		return nil, nil, err
	}
	receipt, err := h.authorizeExactReadDecision(query.WorkspaceID, query.CapabilityRevision, "host.v2.read:tasks", CanonicalApprovalDigest("sessions", query.WorkspaceID, binding.ProjectionVersion, strconv.Itoa(offset), strconv.FormatInt(int64(limit), 10)))
	if err != nil {
		return nil, nil, err
	}
	snapshotVersion, err := h.exactSnapshots.create(binding, 0)
	if err != nil {
		return nil, nil, status.Error(codes.Unavailable, "exact session snapshot is unavailable")
	}
	info := &pluginsdk.ExactPageInfo{SnapshotVersion: snapshotVersion, HasMore: len(rows) > len(items)}
	if info.HasMore {
		info.NextCursor, err = h.exactSnapshots.create(binding, offset+len(items))
		if err != nil {
			return nil, nil, status.Error(codes.Unavailable, "exact session cursor is unavailable")
		}
	}
	if err := h.recordExactPageRead(info, receipt); err != nil {
		return nil, nil, err
	}
	return items, info, nil
}

// GetSessionExact reads one lifecycle projection from the supplied durable
// session snapshot. The snapshot binds the session incarnation and route
// generation, so callers cannot infer current progress from a stale session.
func (h *pluginHost) GetSessionExact(ctx context.Context, query pluginsdk.ExactSessionGetQuery) (*pluginsdk.ExactSession, error) {
	if err := h.authorizeExactRead(query.WorkspaceID, query.CapabilityRevision, "host.v2.read:tasks", CanonicalApprovalDigest("session", query.WorkspaceID, query.SessionID, query.SnapshotVersion)); err != nil {
		return nil, err
	}
	if h.taskData == nil || query.SessionID == "" || query.SnapshotVersion == "" {
		return nil, status.Error(codes.FailedPrecondition, "exact session read is unavailable")
	}
	binding, err := h.exactSessionBinding(query.WorkspaceID, query.CapabilityRevision, query.SnapshotVersion)
	if err != nil {
		return nil, err
	}
	row, err := h.taskData.GetExactSessionSnapshotSession(ctx, binding.ProjectionVersion, query.SessionID)
	if err != nil {
		return nil, exactSessionSnapshotError(err)
	}
	if row == nil || row.WorkspaceID != query.WorkspaceID || row.QueueIncarnationID == "" || row.ResourceVersion <= 0 {
		return nil, status.Error(codes.FailedPrecondition, "exact session projection is incomplete")
	}
	result := exactSessionToDTO(*row)
	return &result, nil
}

func (h *pluginHost) exactSessionSnapshot(ctx context.Context, query pluginsdk.ExactSessionQuery) (exactSnapshotBinding, error) {
	if query.Page.Limit < 0 || query.Page.Limit > exactSessionPageLimit || query.Page.Cursor != "" && query.Page.SnapshotVersion == "" {
		return exactSnapshotBinding{}, status.Error(codes.InvalidArgument, "exact snapshot page is invalid")
	}
	return h.exactSessionSnapshotBinding(ctx, query)
}

func (h *pluginHost) exactSessionSnapshotBinding(ctx context.Context, query pluginsdk.ExactSessionQuery) (exactSnapshotBinding, error) {
	if query.Page.SnapshotVersion != "" {
		return h.exactSessionBinding(query.WorkspaceID, query.CapabilityRevision, query.Page.SnapshotVersion)
	}
	snapshot, err := h.taskData.OpenExactSessionSnapshot(ctx, taskmodels.ExactSessionSnapshotRequest{WorkspaceID: query.WorkspaceID})
	if err != nil {
		return exactSnapshotBinding{}, exactSessionSnapshotError(err)
	}
	if snapshot == nil || snapshot.Token == "" || snapshot.WorkspaceID != query.WorkspaceID {
		return exactSnapshotBinding{}, status.Error(codes.FailedPrecondition, "exact session snapshot is unavailable")
	}
	return h.exactPageBinding(query.WorkspaceID, query.CapabilityRevision, "sessions", snapshot.Token), nil
}

func (h *pluginHost) exactSessionBinding(workspaceID string, revision uint64, snapshotVersion string) (exactSnapshotBinding, error) {
	if h.exactSnapshots == nil || snapshotVersion == "" {
		return exactSnapshotBinding{}, status.Error(codes.FailedPrecondition, "exact session snapshot is unavailable")
	}
	cursor, err := h.exactSnapshots.parse(snapshotVersion)
	want := h.exactPageBinding(workspaceID, revision, "sessions", cursor.ProjectionVersion)
	if err != nil || cursor.Offset != 0 || cursor.exactSnapshotBinding != want {
		return exactSnapshotBinding{}, status.Error(codes.InvalidArgument, "exact session snapshot is invalid")
	}
	return cursor.exactSnapshotBinding, nil
}

func (h *pluginHost) exactSessionOffset(binding exactSnapshotBinding, page pluginsdk.ExactPage) (int, error) {
	if page.Limit < 0 || page.Limit > exactSessionPageLimit {
		return 0, status.Error(codes.InvalidArgument, "exact snapshot page is invalid")
	}
	if page.Cursor == "" {
		return 0, nil
	}
	offset, err := h.exactSnapshots.offset(page.Cursor, binding)
	if err != nil {
		return 0, status.Error(codes.InvalidArgument, "exact session cursor is invalid")
	}
	return offset, nil
}

func exactSessionDTOs(rows []taskmodels.ExactSessionSnapshotSession, workspaceID string, limit int32) ([]pluginsdk.ExactSession, error) {
	items := make([]pluginsdk.ExactSession, min(len(rows), int(limit)))
	for i := range items {
		if rows[i].WorkspaceID != workspaceID {
			return nil, status.Error(codes.FailedPrecondition, "exact session projection is incomplete")
		}
		items[i] = exactSessionToDTO(rows[i])
	}
	return items, nil
}

func exactSessionToDTO(row taskmodels.ExactSessionSnapshotSession) pluginsdk.ExactSession {
	completedAt := ""
	if row.CompletedAt != nil {
		completedAt = row.CompletedAt.UTC().Format(time.RFC3339Nano)
	}
	return pluginsdk.ExactSession{ID: row.ID, TaskID: row.TaskID, WorkspaceID: row.WorkspaceID, QueueIncarnationID: row.QueueIncarnationID, State: string(row.State), RouteGeneration: row.RouteGeneration, StartedAt: row.StartedAt.UTC().Format(time.RFC3339Nano), CompletedAt: completedAt, UpdatedAt: row.UpdatedAt.UTC().Format(time.RFC3339Nano), IsPrimary: row.IsPrimary, ResourceVersion: row.ResourceVersion}
}

func exactSessionSnapshotError(err error) error {
	if errors.Is(err, repoerrors.ErrExactSessionSnapshotUnavailable) {
		return status.Error(codes.FailedPrecondition, "exact session snapshot is unavailable")
	}
	return err
}

var _ pluginsdk.ExactSessionHost = (*pluginHost)(nil)
