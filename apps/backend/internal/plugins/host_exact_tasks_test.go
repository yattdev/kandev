package plugins

import (
	"context"
	"reflect"
	"testing"

	"github.com/kandev/kandev/internal/exactsnapshotcomposite"
	"github.com/kandev/kandev/internal/orchestrator/messagequeue"
	"github.com/kandev/kandev/internal/plugins/manifest"
	taskmodels "github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository/repoerrors"
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
		return ApprovalDecision{Allowed: workspaceID == "workspace-1" && revision == 2 && capabilityID == "host.v2.read:tasks", Receipt: ApprovalReceipt{AuditID: "task-read-audit"}}
	}
	var receipts []ApprovalReceipt
	d.host.exactReadReceipt = func(receipt ApprovalReceipt) error { receipts = append(receipts, receipt); return nil }
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
	require.Empty(t, page.AuditID)
	require.Empty(t, receipts)
	require.NotEmpty(t, page.SnapshotVersion)
	require.NotContains(t, page.SnapshotVersion, "snapshot-workspace-1")
	snapshotVersion := page.SnapshotVersion

	items, page, err = d.host.ListTasksExact(context.Background(), pluginsdk.ExactTaskQuery{WorkspaceID: "workspace-1", CapabilityRevision: 2, Page: pluginsdk.ExactPage{Limit: 1, Cursor: page.NextCursor, SnapshotVersion: page.SnapshotVersion}})
	require.NoError(t, err)
	require.Equal(t, "task-2", items[0].ID)
	require.False(t, page.HasMore)
	require.NotEmpty(t, page.AuditID)
	require.Len(t, receipts, 1)

	task, err := d.host.GetTaskExact(context.Background(), pluginsdk.ExactTaskGetQuery{WorkspaceID: "workspace-1", TaskID: "task-2", CapabilityRevision: 2, SnapshotVersion: snapshotVersion})
	require.NoError(t, err)
	require.Equal(t, int64(4), task.ResourceVersion)

	_, _, err = d.host.ListTasksExact(context.Background(), pluginsdk.ExactTaskQuery{WorkspaceID: "workspace-2", CapabilityRevision: 2})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	_, err = d.host.GetTaskExact(context.Background(), pluginsdk.ExactTaskGetQuery{WorkspaceID: "workspace-2", TaskID: "task-2", CapabilityRevision: 2, SnapshotVersion: "snapshot-workspace-1"})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestPluginHostExactTaskReceiptRequiresCompleteValidRead(t *testing.T) {
	newHost := func() (*testDataHost, *[]ApprovalReceipt) {
		d := newTestDataHost(manifest.Capabilities{})
		d.host.installationID = "installation-1"
		d.host.exactSnapshots = newExactSnapshotStore([]byte("01234567890123456789012345678901"))
		d.host.exactAuthorize = func(workspaceID string, revision uint64, capabilityID, _ string) ApprovalDecision {
			return ApprovalDecision{Allowed: workspaceID == "workspace-1" && revision == 2 && capabilityID == "host.v2.read:tasks", Receipt: ApprovalReceipt{AuditID: "task-read-audit"}}
		}
		receipts := []ApprovalReceipt{}
		d.host.exactReadReceipt = func(receipt ApprovalReceipt) error { receipts = append(receipts, receipt); return nil }
		return d, &receipts
	}

	t.Run("snapshot open failure", func(t *testing.T) {
		d, receipts := newHost()
		d.tasks.exactSnapshotErr = repoerrors.ErrExactTaskSnapshotUnavailable
		_, _, err := d.host.ListTasksExact(context.Background(), pluginsdk.ExactTaskQuery{WorkspaceID: "workspace-1", CapabilityRevision: 2})
		require.Equal(t, codes.FailedPrecondition, status.Code(err))
		require.Empty(t, *receipts)
	})

	t.Run("page validation failure", func(t *testing.T) {
		d, receipts := newHost()
		d.tasks.exactSnapshots = map[string][]taskmodels.ExactTaskSnapshotTask{
			"snapshot-workspace-1": {{ID: "foreign", WorkspaceID: "workspace-2"}},
		}
		_, _, err := d.host.ListTasksExact(context.Background(), pluginsdk.ExactTaskQuery{WorkspaceID: "workspace-1", CapabilityRevision: 2})
		require.Equal(t, codes.FailedPrecondition, status.Code(err))
		require.Empty(t, *receipts)
	})

	t.Run("truncated page and invalid continuation", func(t *testing.T) {
		d, receipts := newHost()
		d.tasks.exactSnapshots = map[string][]taskmodels.ExactTaskSnapshotTask{
			"snapshot-workspace-1": {
				{ID: "task-1", WorkspaceID: "workspace-1"},
				{ID: "task-2", WorkspaceID: "workspace-1"},
			},
		}
		_, page, err := d.host.ListTasksExact(context.Background(), pluginsdk.ExactTaskQuery{WorkspaceID: "workspace-1", CapabilityRevision: 2, Page: pluginsdk.ExactPage{Limit: 1}})
		require.NoError(t, err)
		require.True(t, page.HasMore)
		require.Empty(t, page.AuditID)
		require.Empty(t, *receipts)

		_, _, err = d.host.ListTasksExact(context.Background(), pluginsdk.ExactTaskQuery{WorkspaceID: "workspace-1", CapabilityRevision: 2, Page: pluginsdk.ExactPage{SnapshotVersion: page.SnapshotVersion, Cursor: page.NextCursor + "invalid"}})
		require.Equal(t, codes.InvalidArgument, status.Code(err))
		require.Empty(t, *receipts)
	})
}

type fakeExactDecisionEvidence struct{}

func (fakeExactDecisionEvidence) Open(_ context.Context, request exactsnapshotcomposite.Request) (*exactsnapshotcomposite.Snapshot, error) {
	return &exactsnapshotcomposite.Snapshot{Token: "evidence-1", WorkspaceID: request.WorkspaceID}, nil
}
func (fakeExactDecisionEvidence) Page(_ context.Context, _ string, _, _, pendingOffset, _ int) (*exactsnapshotcomposite.Page, error) {
	items := []messagequeue.ExactPendingTransition{{SessionID: "s", TaskID: "t", WorkspaceID: "workspace-1", SessionIncarnationID: "i", ResourceVersion: 1, TaskResourceVersion: 2, SessionResourceVersion: 3}, {SessionID: "s2", TaskID: "t2", WorkspaceID: "workspace-1", SessionIncarnationID: "i2", ResourceVersion: 2, TaskResourceVersion: 3, SessionResourceVersion: 4}}
	return &exactsnapshotcomposite.Page{PendingTransitions: items[pendingOffset:]}, nil
}

type expiringExactDecisionEvidence struct {
	fakeExactDecisionEvidence
	expired bool
}

func (f *expiringExactDecisionEvidence) Page(ctx context.Context, token string, relationOffset, relationLimit, pendingOffset, pendingLimit int) (*exactsnapshotcomposite.Page, error) {
	if f.expired {
		return nil, exactsnapshotcomposite.ErrUnavailable
	}
	return f.fakeExactDecisionEvidence.Page(ctx, token, relationOffset, relationLimit, pendingOffset, pendingLimit)
}
func TestPluginHostExactDecisionEvidenceUsesReceiptAndCursor(t *testing.T) {
	d := newTestDataHost(manifest.Capabilities{})
	d.host.installationID = "installation-1"
	d.host.exactSnapshots = newExactSnapshotStore([]byte("01234567890123456789012345678901"))
	d.host.exactDecisionEvidence = fakeExactDecisionEvidence{}
	var receipts []ApprovalReceipt
	d.host.exactAuthorize = func(ws string, r uint64, c, _ string) ApprovalDecision {
		return ApprovalDecision{Allowed: ws == "workspace-1" && r == 2 && c == "host.v2.read:tasks", Receipt: ApprovalReceipt{AuditID: "audit-1", Result: "allowed"}}
	}
	d.host.exactReadReceipt = func(receipt ApprovalReceipt) error { receipts = append(receipts, receipt); return nil }
	page, info, err := d.host.ListTaskDecisionEvidenceExact(context.Background(), pluginsdk.ExactTaskDecisionEvidenceQuery{WorkspaceID: "workspace-1", CapabilityRevision: 2, Page: pluginsdk.ExactPage{Limit: 1}})
	require.NoError(t, err)
	require.Len(t, page.PendingTransitions, 1)
	require.Equal(t, "audit-1", info.AuditID)
	require.Len(t, receipts, 1)
	require.True(t, info.HasMore)
	second, secondInfo, err := d.host.ListTaskDecisionEvidenceExact(context.Background(), pluginsdk.ExactTaskDecisionEvidenceQuery{WorkspaceID: "workspace-1", CapabilityRevision: 2, Page: pluginsdk.ExactPage{Limit: 1, SnapshotVersion: info.SnapshotVersion, Cursor: info.NextCursor}})
	require.NoError(t, err)
	require.Len(t, second.PendingTransitions, 1)
	require.False(t, secondInfo.HasMore)
	tamperedSuffix := "x"
	if info.NextCursor[len(info.NextCursor)-1:] == tamperedSuffix {
		tamperedSuffix = "y"
	}
	tampered := info.NextCursor[:len(info.NextCursor)-1] + tamperedSuffix
	_, _, err = d.host.ListTaskDecisionEvidenceExact(context.Background(), pluginsdk.ExactTaskDecisionEvidenceQuery{WorkspaceID: "workspace-1", CapabilityRevision: 2, Page: pluginsdk.ExactPage{Limit: 1, SnapshotVersion: info.SnapshotVersion, Cursor: tampered}})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	_, _, err = d.host.ListTaskDecisionEvidenceExact(context.Background(), pluginsdk.ExactTaskDecisionEvidenceQuery{WorkspaceID: "workspace-2", CapabilityRevision: 2})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
}

// @covers AC-2, AC-3
func TestPluginHostExactDecisionEvidenceRejectsExpiredCompositeSnapshot(t *testing.T) {
	d := newTestDataHost(manifest.Capabilities{})
	d.host.installationID = "installation-1"
	d.host.exactSnapshots = newExactSnapshotStore([]byte("01234567890123456789012345678901"))
	evidence := &expiringExactDecisionEvidence{}
	d.host.exactDecisionEvidence = evidence
	d.host.exactAuthorize = func(ws string, revision uint64, capabilityID, _ string) ApprovalDecision {
		return ApprovalDecision{Allowed: ws == "workspace-1" && revision == 2 && capabilityID == "host.v2.read:tasks", Receipt: ApprovalReceipt{AuditID: "audit-1", Result: "allowed"}}
	}
	d.host.exactReadReceipt = func(ApprovalReceipt) error { return nil }

	_, info, err := d.host.ListTaskDecisionEvidenceExact(context.Background(), pluginsdk.ExactTaskDecisionEvidenceQuery{WorkspaceID: "workspace-1", CapabilityRevision: 2, Page: pluginsdk.ExactPage{Limit: 1}})
	require.NoError(t, err)
	require.True(t, info.HasMore)

	evidence.expired = true
	page, next, err := d.host.ListTaskDecisionEvidenceExact(context.Background(), pluginsdk.ExactTaskDecisionEvidenceQuery{WorkspaceID: "workspace-1", CapabilityRevision: 2, Page: pluginsdk.ExactPage{Limit: 1, SnapshotVersion: info.SnapshotVersion, Cursor: info.NextCursor}})
	require.Nil(t, page)
	require.Nil(t, next)
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
}

// @covers AC-3
func TestPluginHostExactDecisionEvidenceUsesLateServiceWiring(t *testing.T) {
	svc := NewService(nil, NewRegistry(), nil, testLogger(t))
	host := svc.hostForPlugin("missing-plugin")
	exact, ok := pluginsdk.ExactTaskDecisionEvidence(host)
	require.True(t, ok)

	svc.SetExactTaskDecisionEvidence(fakeExactDecisionEvidence{})

	concrete := host.(*pluginHost)
	concrete.installationID = "installation-1"
	concrete.exactSnapshots = newExactSnapshotStore([]byte("01234567890123456789012345678901"))
	concrete.exactAuthorize = func(ws string, revision uint64, capabilityID, _ string) ApprovalDecision {
		return ApprovalDecision{Allowed: ws == "workspace-1" && revision == 2 && capabilityID == "host.v2.read:tasks", Receipt: ApprovalReceipt{AuditID: "audit-1", Result: "allowed"}}
	}
	concrete.exactReadReceipt = func(ApprovalReceipt) error { return nil }

	page, _, err := exact.ListTaskDecisionEvidenceExact(context.Background(), pluginsdk.ExactTaskDecisionEvidenceQuery{WorkspaceID: "workspace-1", CapabilityRevision: 2, Page: pluginsdk.ExactPage{Limit: 1}})
	require.NoError(t, err)
	require.Len(t, page.PendingTransitions, 1)
}
