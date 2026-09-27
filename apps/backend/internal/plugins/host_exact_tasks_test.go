package plugins

import (
	"context"
	"reflect"
	"testing"

	"github.com/kandev/kandev/internal/plugins/manifest"
	taskmodels "github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/pkg/pluginsdk"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// @covers AC-2
func TestPluginHostExposesExactTaskReadMethods(t *testing.T) {
	hostType := reflect.TypeOf(&pluginHost{})
	for _, name := range []string{"ListTasksExact", "GetTaskExact"} {
		if _, found := hostType.MethodByName(name); !found {
			t.Fatalf("%s is required for the additive exact task-read boundary", name)
		}
	}
}

// @covers AC-2
func TestPluginHostExactTasksUseApprovalBoundDurableSnapshot(t *testing.T) {
	d := newTestDataHost(manifest.Capabilities{})
	d.host.installationID = "installation-1"
	d.host.exactSnapshots = newExactSnapshotStore([]byte("01234567890123456789012345678901"))
	d.host.exactAuthorize = func(workspaceID string, revision uint64, capabilityID, _ string) ApprovalDecision {
		return ApprovalDecision{Allowed: workspaceID == "workspace-1" && revision == 2 && capabilityID == "host.v2.read:tasks"}
	}
	d.host.exactReadReceipt = func(ApprovalReceipt) error { return nil }
	d.tasks.exactSnapshots = map[string][]taskmodels.ExactTaskSnapshotTask{
		"snapshot-workspace-1": {
			{ID: "task-1", WorkspaceID: "workspace-1", WorkflowID: "workflow-1", WorkflowStepID: "step-1", Title: "Blocked", State: "TODO", Priority: "high", ResourceVersion: 3},
			{ID: "task-2", WorkspaceID: "workspace-1", WorkflowID: "workflow-1", WorkflowStepID: "step-2", Title: "Done", State: "COMPLETED", Priority: "low", Archived: true, ResourceVersion: 4},
		},
	}

	items, page, err := d.host.ListTasksExact(context.Background(), pluginsdk.ExactTaskQuery{WorkspaceID: "workspace-1", CapabilityRevision: 2, Page: pluginsdk.ExactPage{Limit: 1}})
	require.NoError(t, err)
	require.Equal(t, []pluginsdk.ExactTask{{ID: "task-1", WorkspaceID: "workspace-1", WorkflowID: "workflow-1", WorkflowStepID: "step-1", Title: "Blocked", State: "TODO", Priority: "high", ResourceVersion: 3}}, items)
	require.True(t, page.HasMore)
	require.NotEmpty(t, page.SnapshotVersion)
	require.NotContains(t, page.SnapshotVersion, "snapshot-workspace-1")
	snapshotVersion := page.SnapshotVersion

	items, page, err = d.host.ListTasksExact(context.Background(), pluginsdk.ExactTaskQuery{WorkspaceID: "workspace-1", CapabilityRevision: 2, Page: pluginsdk.ExactPage{Limit: 1, Cursor: page.NextCursor, SnapshotVersion: page.SnapshotVersion}})
	require.NoError(t, err)
	require.Equal(t, "task-2", items[0].ID)
	require.False(t, page.HasMore)

	task, err := d.host.GetTaskExact(context.Background(), pluginsdk.ExactTaskGetQuery{WorkspaceID: "workspace-1", TaskID: "task-2", CapabilityRevision: 2, SnapshotVersion: snapshotVersion})
	require.NoError(t, err)
	require.Equal(t, int64(4), task.ResourceVersion)

	_, _, err = d.host.ListTasksExact(context.Background(), pluginsdk.ExactTaskQuery{WorkspaceID: "workspace-2", CapabilityRevision: 2})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	_, err = d.host.GetTaskExact(context.Background(), pluginsdk.ExactTaskGetQuery{WorkspaceID: "workspace-2", TaskID: "task-2", CapabilityRevision: 2, SnapshotVersion: "snapshot-workspace-1"})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
}
