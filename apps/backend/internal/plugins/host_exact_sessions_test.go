package plugins

import (
	"context"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/plugins/manifest"
	taskmodels "github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository/repoerrors"
	"github.com/kandev/kandev/pkg/pluginsdk"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// @covers AC-2
func TestPluginHostExactSessionsUseApprovalBoundDurableSnapshot(t *testing.T) {
	d := newTestDataHost(manifest.Capabilities{})
	d.host.installationID = "installation-1"
	d.host.exactSnapshots = newExactSnapshotStore([]byte("01234567890123456789012345678901"))
	d.host.exactAuthorize = func(workspaceID string, revision uint64, capabilityID, _ string) ApprovalDecision {
		return ApprovalDecision{Allowed: workspaceID == "workspace-1" && revision == 2 && capabilityID == "host.v2.read:tasks", Receipt: ApprovalReceipt{AuditID: "audit-1", Result: "allowed"}}
	}
	var receipts []ApprovalReceipt
	d.host.exactReadReceipt = func(receipt ApprovalReceipt) error { receipts = append(receipts, receipt); return nil }
	startedAt := time.Date(2026, 9, 28, 3, 0, 0, 0, time.UTC)
	d.tasks.exactSessions = map[string][]taskmodels.ExactSessionSnapshotSession{
		"sessions-workspace-1": {
			{ID: "session-1", TaskID: "task-blocked", WorkspaceID: "workspace-1", QueueIncarnationID: "generation-1", State: taskmodels.TaskSessionState("RUNNING"), RouteGeneration: 2, StartedAt: startedAt, UpdatedAt: startedAt, IsPrimary: true, ResourceVersion: 3},
			{ID: "session-2", TaskID: "task-done", WorkspaceID: "workspace-1", QueueIncarnationID: "generation-2", State: taskmodels.TaskSessionState("COMPLETED"), RouteGeneration: 3, StartedAt: startedAt, CompletedAt: &startedAt, UpdatedAt: startedAt, ResourceVersion: 4},
		},
	}

	items, page, err := d.host.ListSessionsExact(context.Background(), pluginsdk.ExactSessionQuery{WorkspaceID: "workspace-1", CapabilityRevision: 2, Page: pluginsdk.ExactPage{Limit: 1}})
	require.NoError(t, err)
	require.Equal(t, "session-1", items[0].ID)
	require.Equal(t, int64(3), items[0].ResourceVersion)
	require.Equal(t, "audit-1", page.AuditID)
	require.True(t, page.HasMore)
	progress, err := d.host.GetSessionExact(context.Background(), pluginsdk.ExactSessionGetQuery{WorkspaceID: "workspace-1", SessionID: "session-1", CapabilityRevision: 2, SnapshotVersion: page.SnapshotVersion})
	require.NoError(t, err)
	require.Equal(t, "RUNNING", progress.State)
	require.Equal(t, "generation-1", progress.QueueIncarnationID)
	require.Equal(t, int64(2), progress.RouteGeneration)
	_, err = d.host.GetSessionExact(context.Background(), pluginsdk.ExactSessionGetQuery{WorkspaceID: "workspace-2", SessionID: "session-1", CapabilityRevision: 2, SnapshotVersion: page.SnapshotVersion})
	require.Equal(t, codes.PermissionDenied, status.Code(err))

	items, next, err := d.host.ListSessionsExact(context.Background(), pluginsdk.ExactSessionQuery{WorkspaceID: "workspace-1", CapabilityRevision: 2, Page: pluginsdk.ExactPage{Limit: 1, SnapshotVersion: page.SnapshotVersion, Cursor: page.NextCursor}})
	require.NoError(t, err)
	require.Equal(t, "session-2", items[0].ID)
	require.False(t, next.HasMore)
	require.Len(t, receipts, 3)

	_, _, err = d.host.ListSessionsExact(context.Background(), pluginsdk.ExactSessionQuery{WorkspaceID: "workspace-2", CapabilityRevision: 2})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
}

// @covers AC-2
func TestPluginHostExactSessionsRejectsExpiredSnapshot(t *testing.T) {
	d := newTestDataHost(manifest.Capabilities{})
	d.host.installationID = "installation-1"
	d.host.exactSnapshots = newExactSnapshotStore([]byte("01234567890123456789012345678901"))
	d.host.exactAuthorize = func(_ string, _ uint64, _ string, _ string) ApprovalDecision {
		return ApprovalDecision{Allowed: true, Receipt: ApprovalReceipt{AuditID: "audit-1"}}
	}
	d.host.exactReadReceipt = func(ApprovalReceipt) error { return nil }
	d.tasks.exactSessions = map[string][]taskmodels.ExactSessionSnapshotSession{"sessions-workspace-1": {{ID: "session-1", WorkspaceID: "workspace-1"}}}

	_, page, err := d.host.ListSessionsExact(context.Background(), pluginsdk.ExactSessionQuery{WorkspaceID: "workspace-1", CapabilityRevision: 2})
	require.NoError(t, err)
	d.tasks.exactSessionErr = repoerrors.ErrExactSessionSnapshotUnavailable

	items, info, err := d.host.ListSessionsExact(context.Background(), pluginsdk.ExactSessionQuery{WorkspaceID: "workspace-1", CapabilityRevision: 2, Page: pluginsdk.ExactPage{SnapshotVersion: page.SnapshotVersion}})
	require.Nil(t, items)
	require.Nil(t, info)
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
}
