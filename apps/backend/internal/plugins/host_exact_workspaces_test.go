package plugins

import (
	"context"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/plugins/manifest"
	taskmodels "github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/pkg/pluginsdk"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestPluginHost_ListWorkspacesExactAuthorizesAndBindsSnapshot(t *testing.T) {
	d := newTestDataHost(manifest.Capabilities{})
	updatedAt := time.Date(2026, 9, 27, 21, 0, 0, 123, time.UTC)
	d.tasks.workspaces = []*taskmodels.Workspace{{ID: "workspace-1", Name: "Exact", UpdatedAt: updatedAt}}
	d.host.installationID = "installation-1"
	d.host.exactSnapshots = newExactSnapshotStore([]byte("01234567890123456789012345678901"))
	d.host.exactAuthorize = func(workspaceID string, revision uint64, capabilityID, _ string) ApprovalDecision {
		return ApprovalDecision{Allowed: workspaceID == "workspace-1" && revision == 2 && capabilityID == "host.v2.read:workspaces"}
	}

	query := pluginsdk.ExactWorkspaceQuery{WorkspaceID: "workspace-1", CapabilityRevision: 2, Page: pluginsdk.ExactPage{Limit: 1}}
	workspaces, page, err := d.host.ListWorkspacesExact(context.Background(), query)
	if err != nil {
		t.Fatalf("ListWorkspacesExact() error = %v", err)
	}
	if len(workspaces) != 1 || workspaces[0].ID != "workspace-1" || page.SnapshotVersion == "" || page.HasMore || page.NextCursor != "" {
		t.Fatalf("ListWorkspacesExact() = %#v, %#v", workspaces, page)
	}

	binding := exactSnapshotBinding{InstallationID: "installation-1", WorkspaceID: "workspace-1", FilterDigest: "workspaces", ApprovalRevision: 2, ProjectionVersion: page.SnapshotVersion}
	cursor, err := d.host.exactSnapshots.create(binding, 0)
	if err != nil {
		t.Fatalf("create cursor: %v", err)
	}
	_, _, err = d.host.ListWorkspacesExact(context.Background(), pluginsdk.ExactWorkspaceQuery{WorkspaceID: "workspace-1", CapabilityRevision: 2, Page: pluginsdk.ExactPage{Cursor: cursor, SnapshotVersion: page.SnapshotVersion}})
	if err != nil {
		t.Fatalf("ListWorkspacesExact() cursor error = %v", err)
	}
	tamperedCursor := cursor[:len(cursor)-2] + "A" + cursor[len(cursor)-1:]
	if tamperedCursor == cursor {
		tamperedCursor = cursor[:len(cursor)-2] + "B" + cursor[len(cursor)-1:]
	}

	for _, denial := range []struct {
		name  string
		query pluginsdk.ExactWorkspaceQuery
		code  codes.Code
	}{
		{name: "foreign workspace", query: pluginsdk.ExactWorkspaceQuery{WorkspaceID: "workspace-2", CapabilityRevision: 2}, code: codes.PermissionDenied},
		{name: "stale approval", query: pluginsdk.ExactWorkspaceQuery{WorkspaceID: "workspace-1", CapabilityRevision: 1}, code: codes.PermissionDenied},
		{name: "unbounded page", query: pluginsdk.ExactWorkspaceQuery{WorkspaceID: "workspace-1", CapabilityRevision: 2, Page: pluginsdk.ExactPage{Limit: 2}}, code: codes.InvalidArgument},
		{name: "tampered cursor", query: pluginsdk.ExactWorkspaceQuery{WorkspaceID: "workspace-1", CapabilityRevision: 2, Page: pluginsdk.ExactPage{Cursor: tamperedCursor, SnapshotVersion: page.SnapshotVersion}}, code: codes.InvalidArgument},
	} {
		t.Run(denial.name, func(t *testing.T) {
			_, _, err := d.host.ListWorkspacesExact(context.Background(), denial.query)
			if status.Code(err) != denial.code {
				t.Fatalf("ListWorkspacesExact() error = %v, want %s", err, denial.code)
			}
		})
	}
}
