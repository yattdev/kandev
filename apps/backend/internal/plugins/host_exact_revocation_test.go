package plugins

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/kandev/kandev/internal/plugins/manifest"
	taskmodels "github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/pkg/pluginsdk"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type pausingExactGetSource struct {
	taskDataSource
	entered chan struct{}
	release chan struct{}
}

func (s *pausingExactGetSource) GetExactTaskSnapshotTask(ctx context.Context, token, taskID string) (*taskmodels.ExactTaskSnapshotTask, error) {
	row, err := s.taskDataSource.GetExactTaskSnapshotTask(ctx, token, taskID)
	close(s.entered)
	<-s.release
	return row, err
}

func (s *pausingExactGetSource) GetExactSessionSnapshotSession(ctx context.Context, token, sessionID string) (*taskmodels.ExactSessionSnapshotSession, error) {
	row, err := s.taskDataSource.GetExactSessionSnapshotSession(ctx, token, sessionID)
	close(s.entered)
	<-s.release
	return row, err
}

func TestExactSingleItemReadsRejectRevocationDuringFetch(t *testing.T) {
	for _, kind := range []string{"task", "session"} {
		t.Run(kind, func(t *testing.T) {
			d := newTestDataHost(manifest.Capabilities{})
			d.host.installationID = "installation-1"
			d.host.exactSnapshots = newExactSnapshotStore([]byte("01234567890123456789012345678901"))
			var allowed atomic.Bool
			allowed.Store(true)
			d.host.exactAuthorize = func(_ string, _ uint64, _, _ string) ApprovalDecision {
				return ApprovalDecision{Allowed: allowed.Load(), Receipt: ApprovalReceipt{AuditID: "read-audit", Result: approvalReceiptAllowed}}
			}
			var receipts atomic.Int32
			d.host.exactReadReceipt = func(ApprovalReceipt) error { receipts.Add(1); return nil }
			d.tasks.exactSnapshots = map[string][]taskmodels.ExactTaskSnapshotTask{
				"snapshot-workspace-1": {{ID: "task-1", WorkspaceID: "workspace-1", ResourceVersion: 1}},
			}
			d.tasks.exactSessions = map[string][]taskmodels.ExactSessionSnapshotSession{
				"sessions-workspace-1": {{ID: "session-1", WorkspaceID: "workspace-1", QueueIncarnationID: "generation-1", ResourceVersion: 1}},
			}
			var snapshotVersion string
			if kind == "task" {
				_, page, err := d.host.ListTasksExact(context.Background(), pluginsdk.ExactTaskQuery{WorkspaceID: "workspace-1", CapabilityRevision: 1})
				require.NoError(t, err)
				snapshotVersion = page.SnapshotVersion
			} else {
				_, page, err := d.host.ListSessionsExact(context.Background(), pluginsdk.ExactSessionQuery{WorkspaceID: "workspace-1", CapabilityRevision: 1})
				require.NoError(t, err)
				snapshotVersion = page.SnapshotVersion
			}
			paused := &pausingExactGetSource{taskDataSource: d.host.taskData, entered: make(chan struct{}), release: make(chan struct{})}
			d.host.taskData = paused
			result := make(chan error, 1)
			go func() {
				if kind == "task" {
					_, err := d.host.GetTaskExact(context.Background(), pluginsdk.ExactTaskGetQuery{WorkspaceID: "workspace-1", TaskID: "task-1", SnapshotVersion: snapshotVersion, CapabilityRevision: 1})
					result <- err
					return
				}
				_, err := d.host.GetSessionExact(context.Background(), pluginsdk.ExactSessionGetQuery{WorkspaceID: "workspace-1", SessionID: "session-1", SnapshotVersion: snapshotVersion, CapabilityRevision: 1})
				result <- err
			}()
			<-paused.entered
			allowed.Store(false)
			close(paused.release)
			require.Equal(t, codes.PermissionDenied, status.Code(<-result))
			require.Equal(t, int32(1), receipts.Load(), "revoked fetch must not record a completed read")
		})
	}
}
