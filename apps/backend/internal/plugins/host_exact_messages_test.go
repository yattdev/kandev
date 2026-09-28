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

func TestPluginHostExactSessionMessagesBindApprovalSnapshotAndContinuation(t *testing.T) {
	d := newTestDataHost(manifest.Capabilities{})
	d.host.installationID = "installation-1"
	d.host.exactSnapshots = newExactSnapshotStore([]byte("01234567890123456789012345678901"))
	d.host.exactAuthorize = func(workspace string, revision uint64, capability, _ string) ApprovalDecision {
		return ApprovalDecision{Allowed: workspace == "workspace-1" && revision == 2 && capability == "host.v2.read:tasks", Receipt: ApprovalReceipt{AuditID: "audit"}}
	}
	d.host.exactReadReceipt = func(ApprovalReceipt) error { return nil }
	d.tasks.exactMessages = map[string][]taskmodels.ExactSessionMessageSnapshotMessage{"messages-workspace-1-task-1-session-1": {{ID: "one", Content: "safe", CreatedAt: time.Now().UTC()}, {ID: "two", Content: "safe-two", CreatedAt: time.Now().UTC()}}}
	query := pluginsdk.ExactSessionMessageQuery{WorkspaceID: "workspace-1", TaskID: "task-1", SessionID: "session-1", CapabilityRevision: 2, Page: pluginsdk.ExactPage{Limit: 1}}
	items, page, err := d.host.ListSessionMessagesExact(context.Background(), query)
	require.NoError(t, err)
	require.Equal(t, "one", items[0].ID)
	require.True(t, page.HasMore)
	require.Equal(t, "audit", page.AuditID)
	items, page2, err := d.host.ListSessionMessagesExact(context.Background(), pluginsdk.ExactSessionMessageQuery{WorkspaceID: "workspace-1", TaskID: "task-1", SessionID: "session-1", CapabilityRevision: 2, Page: pluginsdk.ExactPage{Limit: 1, SnapshotVersion: page.SnapshotVersion, Cursor: page.NextCursor}})
	require.NoError(t, err)
	require.Equal(t, "two", items[0].ID)
	require.False(t, page2.HasMore)
	_, _, err = d.host.ListSessionMessagesExact(context.Background(), pluginsdk.ExactSessionMessageQuery{WorkspaceID: "workspace-1", TaskID: "task-2", SessionID: "session-1", CapabilityRevision: 2, Page: pluginsdk.ExactPage{SnapshotVersion: page.SnapshotVersion}})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	_, _, err = d.host.ListSessionMessagesExact(context.Background(), pluginsdk.ExactSessionMessageQuery{WorkspaceID: "workspace-2", TaskID: "task-1", SessionID: "session-1", CapabilityRevision: 2})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestPluginHostExactSessionMessagesFailsClosedOnSnapshotExpiry(t *testing.T) {
	d := newTestDataHost(manifest.Capabilities{})
	d.host.installationID = "installation-1"
	d.host.exactSnapshots = newExactSnapshotStore([]byte("01234567890123456789012345678901"))
	d.host.exactAuthorize = func(string, uint64, string, string) ApprovalDecision {
		return ApprovalDecision{Allowed: true, Receipt: ApprovalReceipt{AuditID: "audit"}}
	}
	d.host.exactReadReceipt = func(ApprovalReceipt) error { return nil }
	d.tasks.exactMessages = map[string][]taskmodels.ExactSessionMessageSnapshotMessage{"messages-workspace-1-task-1-session-1": {{ID: "one"}}}
	_, page, err := d.host.ListSessionMessagesExact(context.Background(), pluginsdk.ExactSessionMessageQuery{WorkspaceID: "workspace-1", TaskID: "task-1", SessionID: "session-1", CapabilityRevision: 2})
	require.NoError(t, err)
	d.tasks.exactMessageErr = repoerrors.ErrExactSessionMessageSnapshotUnavailable
	_, _, err = d.host.ListSessionMessagesExact(context.Background(), pluginsdk.ExactSessionMessageQuery{WorkspaceID: "workspace-1", TaskID: "task-1", SessionID: "session-1", CapabilityRevision: 2, Page: pluginsdk.ExactPage{SnapshotVersion: page.SnapshotVersion}})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
}
