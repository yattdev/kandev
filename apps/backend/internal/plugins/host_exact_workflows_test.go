package plugins

import (
	"context"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/plugins/manifest"
	taskmodels "github.com/kandev/kandev/internal/task/models"
	wfmodels "github.com/kandev/kandev/internal/workflow/models"
	"github.com/kandev/kandev/pkg/pluginsdk"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestPluginHost_ListWorkflowsExactBindsAuthorityAndCursor(t *testing.T) {
	d := newTestDataHost(manifest.Capabilities{})
	now := time.Date(2026, 9, 27, 22, 0, 0, 0, time.UTC)
	d.workflows.workflows = map[string][]*taskmodels.Workflow{"workspace-1": {{ID: "workflow-1", WorkspaceID: "workspace-1", UpdatedAt: now}, {ID: "workflow-2", WorkspaceID: "workspace-1", UpdatedAt: now.Add(time.Second)}}}
	d.steps.steps = map[string][]*wfmodels.WorkflowStep{"workflow-1": {{ID: "step-1", WorkflowID: "workflow-1", Position: 1}}}
	d.host.installationID = "installation-1"
	d.host.exactSnapshots = newExactSnapshotStore([]byte("01234567890123456789012345678901"))
	d.host.exactAuthorize = func(workspaceID string, revision uint64, capabilityID, _ string) ApprovalDecision {
		return ApprovalDecision{Allowed: workspaceID == "workspace-1" && revision == 2 && capabilityID == "host.v2.read:workflows"}
	}

	items, page, err := d.host.ListWorkflowsExact(context.Background(), pluginsdk.ExactWorkflowQuery{WorkspaceID: "workspace-1", CapabilityRevision: 2, Page: pluginsdk.ExactPage{Limit: 1}})
	if err != nil || len(items) != 1 || items[0].ID != "workflow-1" || !page.HasMore || page.NextCursor == "" {
		t.Fatalf("first exact workflows = %#v %#v, %v", items, page, err)
	}
	items, page, err = d.host.ListWorkflowsExact(context.Background(), pluginsdk.ExactWorkflowQuery{WorkspaceID: "workspace-1", CapabilityRevision: 2, Page: pluginsdk.ExactPage{Limit: 1, Cursor: page.NextCursor, SnapshotVersion: page.SnapshotVersion}})
	if err != nil || len(items) != 1 || items[0].ID != "workflow-2" || page.HasMore {
		t.Fatalf("second exact workflows = %#v %#v, %v", items, page, err)
	}

	for _, query := range []pluginsdk.ExactWorkflowQuery{{WorkspaceID: "workspace-2", CapabilityRevision: 2}, {WorkspaceID: "workspace-1", CapabilityRevision: 1}, {WorkspaceID: "workspace-1", CapabilityRevision: 2, Page: pluginsdk.ExactPage{Cursor: "bad", SnapshotVersion: "drift"}}} {
		_, _, err := d.host.ListWorkflowsExact(context.Background(), query)
		if status.Code(err) != codes.PermissionDenied && status.Code(err) != codes.InvalidArgument {
			t.Fatalf("denial error = %v", err)
		}
	}

	steps, stepPage, err := d.host.ListWorkflowStepsExact(context.Background(), pluginsdk.ExactWorkflowStepsQuery{WorkspaceID: "workspace-1", WorkflowID: "workflow-1", CapabilityRevision: 2})
	if err != nil || len(steps) != 1 || stepPage.SnapshotVersion == "" {
		t.Fatalf("exact workflow steps = %#v %#v, %v", steps, stepPage, err)
	}
	_, _, err = d.host.ListWorkflowStepsExact(context.Background(), pluginsdk.ExactWorkflowStepsQuery{WorkspaceID: "workspace-1", WorkflowID: "unknown", CapabilityRevision: 2})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("unknown workflow error = %v", err)
	}
}
