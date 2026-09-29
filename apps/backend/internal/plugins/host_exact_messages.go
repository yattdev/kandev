package plugins

import (
	"context"
	"errors"
	"fmt"
	"time"

	taskmodels "github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository/repoerrors"
	"github.com/kandev/kandev/pkg/pluginsdk"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const exactSessionMessagePageLimit int32 = 100

// ListSessionMessagesExact reads the private materialized transcript through
// an approval-bound, connection-scoped snapshot cursor.
func (h *pluginHost) ListSessionMessagesExact(ctx context.Context, query pluginsdk.ExactSessionMessageQuery) ([]pluginsdk.ExactSessionMessage, *pluginsdk.ExactPageInfo, error) {
	receipt, err := h.authorizeExactReadDecision(query.WorkspaceID, query.CapabilityRevision, "host.v2.read:tasks", CanonicalApprovalDigest("session-messages", query.WorkspaceID, query.TaskID, query.SessionID, query.Page.SnapshotVersion))
	if err != nil {
		return nil, nil, err
	}
	data, err := h.exactSessionMessageData(query)
	if err != nil {
		return nil, nil, status.Error(codes.FailedPrecondition, "exact session message read is unavailable")
	}
	binding, err := h.exactSessionMessageBinding(ctx, data, query)
	if err != nil {
		return nil, nil, err
	}
	offset := 0
	if query.Page.Cursor != "" {
		offset, err = h.exactSnapshots.offset(query.Page.Cursor, binding)
		if err != nil {
			return nil, nil, status.Error(codes.InvalidArgument, "exact session message cursor is invalid")
		}
	}
	limit := exactSessionMessagePageLimit
	if query.Page.Limit > 0 {
		limit = query.Page.Limit
	}
	rows, err := data.PageExactSessionMessageSnapshot(ctx, h.installationID, query.WorkspaceID, query.TaskID, query.SessionID, binding.ProjectionVersion, offset, int(limit)+1)
	if err != nil {
		return nil, nil, exactSessionMessageSnapshotError(err)
	}
	items := make([]pluginsdk.ExactSessionMessage, min(len(rows), int(limit)))
	for i := range items {
		items[i] = pluginsdk.ExactSessionMessage{ID: rows[i].ID, AuthorType: string(rows[i].AuthorType), Content: rows[i].Content, Type: string(rows[i].Type), RequestsInput: rows[i].RequestsInput, CreatedAt: rows[i].CreatedAt.UTC().Format(time.RFC3339Nano), UpdatedAt: rows[i].UpdatedAt.UTC().Format(time.RFC3339Nano)}
	}
	version, err := h.exactSnapshots.create(binding, 0)
	if err != nil {
		return nil, nil, status.Error(codes.Unavailable, "exact session message snapshot is unavailable")
	}
	info := &pluginsdk.ExactPageInfo{SnapshotVersion: version, HasMore: len(rows) > len(items)}
	if info.HasMore {
		info.NextCursor, err = h.exactSnapshots.create(binding, offset+len(items))
		if err != nil {
			return nil, nil, status.Error(codes.Unavailable, "exact session message cursor is unavailable")
		}
	}
	if err := h.recordExactPageRead(info, receipt); err != nil {
		return nil, nil, err
	}
	return items, info, nil
}

type exactSessionMessageData interface {
	OpenExactSessionMessageSnapshot(context.Context, string, string, string, string) (*taskmodels.ExactSessionMessageSnapshot, error)
	PageExactSessionMessageSnapshot(context.Context, string, string, string, string, string, int, int) ([]taskmodels.ExactSessionMessageSnapshotMessage, error)
}

func (h *pluginHost) exactSessionMessageData(query pluginsdk.ExactSessionMessageQuery) (exactSessionMessageData, error) {
	data, ok := h.taskData.(exactSessionMessageData)
	if !ok || h.exactSnapshots == nil || query.TaskID == "" || query.SessionID == "" || query.Page.Limit < 0 || query.Page.Limit > exactSessionMessagePageLimit || query.Page.Cursor != "" && query.Page.SnapshotVersion == "" {
		return nil, errors.New("exact session message data unavailable")
	}
	return data, nil
}

func (h *pluginHost) exactSessionMessageBinding(ctx context.Context, data exactSessionMessageData, query pluginsdk.ExactSessionMessageQuery) (exactSnapshotBinding, error) {
	filter := fmt.Sprintf("session-messages:%s:%s", query.TaskID, query.SessionID)
	if query.Page.SnapshotVersion != "" {
		cursor, err := h.exactSnapshots.parse(query.Page.SnapshotVersion)
		want := h.exactPageBinding(query.WorkspaceID, query.CapabilityRevision, filter, cursor.ProjectionVersion)
		if err != nil || cursor.Offset != 0 || cursor.exactSnapshotBinding != want {
			return exactSnapshotBinding{}, status.Error(codes.InvalidArgument, "exact session message snapshot is invalid")
		}
		return cursor.exactSnapshotBinding, nil
	}
	snapshot, err := data.OpenExactSessionMessageSnapshot(ctx, h.installationID, query.WorkspaceID, query.TaskID, query.SessionID)
	if err != nil {
		return exactSnapshotBinding{}, exactSessionMessageSnapshotError(err)
	}
	if snapshot == nil || snapshot.Token == "" || snapshot.InstallationID != h.installationID || snapshot.WorkspaceID != query.WorkspaceID || snapshot.TaskID != query.TaskID || snapshot.SessionID != query.SessionID {
		return exactSnapshotBinding{}, status.Error(codes.FailedPrecondition, "exact session message snapshot is unavailable")
	}
	return h.exactPageBinding(query.WorkspaceID, query.CapabilityRevision, filter, snapshot.Token), nil
}

func exactSessionMessageSnapshotError(err error) error {
	if errors.Is(err, repoerrors.ErrExactSessionMessageSnapshotUnavailable) {
		return status.Error(codes.FailedPrecondition, "exact session message snapshot is unavailable")
	}
	return err
}

var _ pluginsdk.ExactSessionMessageHost = (*pluginHost)(nil)
