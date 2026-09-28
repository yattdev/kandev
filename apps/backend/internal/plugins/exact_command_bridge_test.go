package plugins

import (
	"context"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/exactsnapshotauthority"
	"github.com/kandev/kandev/internal/exactsnapshotcomposite"
	officesqlite "github.com/kandev/kandev/internal/office/repository/sqlite"
	"github.com/kandev/kandev/internal/orchestrator/messagequeue"
	taskmodels "github.com/kandev/kandev/internal/task/models"
	tasksqlite "github.com/kandev/kandev/internal/task/repository/sqlite"
	"github.com/kandev/kandev/pkg/pluginsdk"
)

type recordingExactCommandBridge struct {
	grants, revokes []CapabilityApproval
	installations   []string
	receipts        []ApprovalReceipt
	err             error
}

func (b *recordingExactCommandBridge) RevokeInstallation(_ context.Context, installationID string) error {
	b.installations = append(b.installations, installationID)
	return b.err
}
func (b *recordingExactCommandBridge) RevokeWorkspace(_ context.Context, installationID, workspaceID string) error {
	b.installations = append(b.installations, installationID+"/"+workspaceID)
	return b.err
}

func (b *recordingExactCommandBridge) Grant(_ context.Context, a CapabilityApproval, _ string) error {
	b.grants = append(b.grants, a)
	return b.err
}
func (b *recordingExactCommandBridge) Revoke(_ context.Context, a CapabilityApproval, _ string) error {
	b.revokes = append(b.revokes, a)
	return b.err
}
func (b *recordingExactCommandBridge) RecordReceipt(_ context.Context, r ApprovalReceipt) error {
	b.receipts = append(b.receipts, r)
	return b.err
}

func TestExactCommandApprovalBridgeProjectsGrantReceiptAndRevoke(t *testing.T) {
	svc := &Service{}
	if err := svc.SetPluginsDir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	bridge := &recordingExactCommandBridge{}
	svc.SetExactTaskCommandApprovalBridge(bridge)
	approval, err := svc.approvalGrant("inst", "workspace", 1, "digest", []string{"host.v2.write:tasks"}, "human", "grant", "grant-audit")
	if err != nil {
		t.Fatal(err)
	}
	if len(bridge.grants) != 1 || bridge.grants[0].Revision != approval.Revision {
		t.Fatalf("grants = %#v", bridge.grants)
	}
	receipt := ApprovalReceipt{InstallationID: "inst", WorkspaceID: "workspace", CapabilityID: "host.v2.write:tasks", Revision: 1, AuditID: "decision-audit", Result: approvalReceiptAllowed, ObservedAt: time.Now().UTC()}
	if err = svc.recordExactReadReceipt(receipt); err != nil {
		t.Fatal(err)
	}
	if len(bridge.receipts) != 1 || bridge.receipts[0].AuditID != receipt.AuditID {
		t.Fatalf("receipts = %#v", bridge.receipts)
	}
	if _, err = svc.approvalRevoke("inst", "workspace", "human", "revoke", "revoke-audit"); err != nil {
		t.Fatal(err)
	}
	if len(bridge.revokes) != 1 || bridge.revokes[0].Revision != 1 {
		t.Fatalf("revokes = %#v", bridge.revokes)
	}
}

func TestSQLiteExactTaskCommandGrantIssuerBindsCompositePendingEvidence(t *testing.T) {
	ctx := context.Background()
	repo, database := newExactCommandCompositionRepository(t)
	office, err := officesqlite.NewWithDB(database, database, nil)
	if err != nil {
		t.Fatal(err)
	}
	queueRepository, err := messagequeue.NewSQLiteRepository(database, database)
	if err != nil {
		t.Fatal(err)
	}
	queue := queueRepository.(interface {
		messagequeue.Repository
		messagequeue.ExactPendingTransitionReader
		messagequeue.ExactPendingTransitionAuthorityReader
	})
	repo.SetExactTaskCommandPendingValidator(queue)
	if err = repo.CreateWorkspace(ctx, &taskmodels.Workspace{ID: "workspace", Name: "workspace"}); err != nil {
		t.Fatal(err)
	}
	if err = repo.CreateTask(ctx, &taskmodels.Task{ID: "task", WorkspaceID: "workspace", Title: "task"}); err != nil {
		t.Fatal(err)
	}
	if err = repo.CreateTaskSession(ctx, &taskmodels.TaskSession{ID: "session", TaskID: "task", State: taskmodels.TaskSessionStateCreated}); err != nil {
		t.Fatal(err)
	}
	if err = queue.SetPendingMove(ctx, "session", &messagequeue.PendingMove{TaskID: "task", WorkflowID: "workflow", WorkflowStepID: "step"}); err != nil {
		t.Fatal(err)
	}
	authority, err := exactsnapshotauthority.NewSQLite(database)
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := exactsnapshotcomposite.New(authority, office, queue)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := evidence.Open(ctx, exactsnapshotcomposite.Request{WorkspaceID: "workspace"})
	if err != nil {
		t.Fatal(err)
	}
	page, err := evidence.Page(ctx, snapshot.Token, 0, 1, 0, 1)
	if err != nil || len(page.PendingTransitions) != 1 {
		t.Fatalf("pending evidence = %#v, %v", page, err)
	}
	approval := tasksqlite.ExactTaskCommandApproval{InstallationID: "installation", WorkspaceID: "workspace", CapabilityID: "host.v2.write:tasks", ReceiptAuditID: "receipt", Revision: 1}
	if err = repo.UpsertExactTaskCommandApproval(ctx, approval); err != nil {
		t.Fatal(err)
	}
	if err = repo.RecordExactTaskCommandReceipt(ctx, approval, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if current, currentErr := evidence.GetPendingTransition(ctx, snapshot.Token, "session"); currentErr != nil || current == nil {
		t.Fatalf("pending evidence after approval projection = %#v, %v", current, currentErr)
	}
	issuer, err := NewSQLiteExactTaskCommandGrantIssuer(repo, evidence)
	if err != nil {
		t.Fatal(err)
	}
	grant := tasksqlite.ExactTaskCommandGrant{ID: "grant", InstallationID: approval.InstallationID, WorkspaceID: approval.WorkspaceID, TaskID: "task", CapabilityID: approval.CapabilityID, ReceiptAuditID: approval.ReceiptAuditID, ApprovalRevision: approval.Revision, ActionDigest: "marker", IdempotencyKey: "key", ExpiresAt: time.Now().UTC().Add(time.Minute)}
	if err = issuer.Issue(ctx, grant, snapshot.Token, page.PendingTransitions[0]); err != nil {
		t.Fatalf("Issue: %v", err)
	}
	var count int
	if err = database.GetContext(ctx, &count, `SELECT COUNT(*) FROM exact_task_command_grants WHERE id = ?`, grant.ID); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("grant count = %d, want 1", count)
	}
}

func TestPluginHostMintsExactTaskGrantFromConnectionBoundEvidence(t *testing.T) {
	ctx := context.Background()
	repo, database := newExactCommandCompositionRepository(t)
	office, err := officesqlite.NewWithDB(database, database, nil)
	if err != nil {
		t.Fatal(err)
	}
	queueRepository, err := messagequeue.NewSQLiteRepository(database, database)
	if err != nil {
		t.Fatal(err)
	}
	queue := queueRepository.(interface {
		messagequeue.Repository
		messagequeue.ExactPendingTransitionReader
		messagequeue.ExactPendingTransitionAuthorityReader
	})
	repo.SetExactTaskCommandPendingValidator(queue)
	if err = repo.CreateWorkspace(ctx, &taskmodels.Workspace{ID: "workspace", Name: "workspace"}); err != nil {
		t.Fatal(err)
	}
	if err = repo.CreateTask(ctx, &taskmodels.Task{ID: "task", WorkspaceID: "workspace", Title: "task"}); err != nil {
		t.Fatal(err)
	}
	if err = repo.CreateTaskSession(ctx, &taskmodels.TaskSession{ID: "session", TaskID: "task", State: taskmodels.TaskSessionStateCreated}); err != nil {
		t.Fatal(err)
	}
	if err = queue.SetPendingMove(ctx, "session", &messagequeue.PendingMove{TaskID: "task", WorkflowID: "workflow", WorkflowStepID: "step"}); err != nil {
		t.Fatal(err)
	}
	authority, err := exactsnapshotauthority.NewSQLite(database)
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := exactsnapshotcomposite.New(authority, office, queue)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := evidence.Open(ctx, exactsnapshotcomposite.Request{WorkspaceID: "workspace"})
	if err != nil {
		t.Fatal(err)
	}
	page, err := evidence.Page(ctx, snapshot.Token, 0, 1, 0, 1)
	if err != nil || len(page.PendingTransitions) != 1 {
		t.Fatalf("pending evidence = %#v, %v", page, err)
	}
	approval := tasksqlite.ExactTaskCommandApproval{InstallationID: "installation", WorkspaceID: "workspace", CapabilityID: "host.v2.write:tasks", ReceiptAuditID: "receipt", Revision: 1}
	if err = repo.UpsertExactTaskCommandApproval(ctx, approval); err != nil {
		t.Fatal(err)
	}
	issuer, err := NewSQLiteExactTaskCommandGrantIssuer(repo, evidence)
	if err != nil {
		t.Fatal(err)
	}
	snapshots := newExactSnapshotStore([]byte("connection-bound-test-secret"))
	host := &pluginHost{
		installationID: "installation",
		exactSnapshots: snapshots,
		exactAuthorize: func(workspaceID string, revision uint64, capabilityID, _ string) ApprovalDecision {
			if workspaceID != "workspace" || revision != 1 || capabilityID != "host.v2.write:tasks" {
				return ApprovalDecision{}
			}
			return ApprovalDecision{Allowed: true, Receipt: ApprovalReceipt{InstallationID: "installation", WorkspaceID: "workspace", CapabilityID: "host.v2.write:tasks", Revision: 1, AuditID: "receipt", Result: approvalReceiptAllowed, ObservedAt: time.Now().UTC()}}
		},
		exactReadReceipt: func(receipt ApprovalReceipt) error {
			return repo.RecordExactTaskCommandReceipt(ctx, tasksqlite.ExactTaskCommandApproval{InstallationID: receipt.InstallationID, WorkspaceID: receipt.WorkspaceID, CapabilityID: receipt.CapabilityID, ReceiptAuditID: receipt.AuditID, Revision: receipt.Revision}, receipt.ObservedAt)
		},
		exactTaskCommandGrantIssuerDep: func() ExactTaskCommandGrantIssuer { return issuer },
	}
	version, err := snapshots.create(host.exactPageBinding("workspace", 1, "task-decision-evidence", snapshot.Token), 0)
	if err != nil {
		t.Fatal(err)
	}
	pending := page.PendingTransitions[0]
	task, err := repo.GetTask(ctx, "task")
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := host.executeExactTaskCommand(ctx, exactTaskCommandGrantRequest{WorkspaceID: "workspace", TaskID: "task", CapabilityRevision: 1, DecisionEvidenceSnapshotVersion: version, PendingTransition: pluginsdk.ExactPendingTaskTransition{SessionID: pending.SessionID, TaskID: pending.TaskID, WorkspaceID: pending.WorkspaceID, SessionIncarnationID: pending.SessionIncarnationID, WorkflowID: pending.WorkflowID, WorkflowStepID: pending.WorkflowStepID, StepPosition: int32(pending.Position), ResourceVersion: pending.ResourceVersion, TaskResourceVersion: pending.TaskResourceVersion, SessionResourceVersion: pending.SessionResourceVersion, QueueGeneration: pending.QueueGeneration, QueuedAt: pending.QueuedAt.UTC().Format(time.RFC3339Nano)}, Marker: "[marker]", IdempotencyKey: "key"}, task.ResourceVersion)
	if err != nil {
		t.Fatalf("execute host command: %v", err)
	}
	if receipt.AuditID != "key" || receipt.ResourceVersion != task.ResourceVersion+1 {
		t.Fatalf("receipt = %+v", receipt)
	}
	var count int
	if err = database.GetContext(ctx, &count, `SELECT COUNT(*) FROM exact_task_command_grants WHERE installation_id = ? AND workspace_id = ? AND idempotency_key = ?`, "installation", "workspace", "key"); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("grant count = %d, want 1", count)
	}
}
