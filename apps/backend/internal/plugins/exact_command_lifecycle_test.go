package plugins

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/kandev/kandev/internal/db"
	"github.com/kandev/kandev/internal/exactsnapshotauthority"
	"github.com/kandev/kandev/internal/exactsnapshotcomposite"
	officesqlite "github.com/kandev/kandev/internal/office/repository/sqlite"
	"github.com/kandev/kandev/internal/orchestrator/messagequeue"
	taskmodels "github.com/kandev/kandev/internal/task/models"
	tasksqlite "github.com/kandev/kandev/internal/task/repository/sqlite"
	"github.com/kandev/kandev/pkg/pluginsdk"
)

func TestExactCommandComposedIssuerRejectsStaleExpiredAndRevokedEvidence(t *testing.T) {
	for name, mutate := range map[string]func(t *testing.T, fixture *exactIssuerFixture){
		"stale pending": func(t *testing.T, f *exactIssuerFixture) {
			t.Helper()
			if _, err := f.queue.TakePendingMove(f.ctx, "session"); err != nil {
				t.Fatal(err)
			}
		},
		"expired composite": func(t *testing.T, f *exactIssuerFixture) {
			t.Helper()
			if _, err := f.database.Exec(`UPDATE exact_composite_snapshots SET expires_at = ? WHERE token = ?`, time.Now().UTC().Add(-time.Minute), f.snapshotToken); err != nil {
				t.Fatal(err)
			}
		},
		"revoked approval": func(t *testing.T, f *exactIssuerFixture) {
			t.Helper()
			if err := f.repo.RevokeExactTaskCommandApproval(f.ctx, f.approval); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newExactIssuerFixture(t)
			mutate(t, f)
			if err := f.issuer.Issue(f.ctx, f.grant, f.snapshotToken, f.pending); err == nil {
				t.Fatal("changed evidence issued a grant")
			}
			f.assertNoEffect(t)
		})
	}
}

func TestExactCommandComposedIssuerConcurrentSameKeyHasOneGrant(t *testing.T) {
	f := newExactIssuerFixture(t)
	var wg sync.WaitGroup
	start := make(chan struct{})
	errorsByAttempt := make([]error, 2)
	for i := range errorsByAttempt {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			grant := f.grant
			if index == 1 {
				grant.ID = "competing-grant"
			}
			errorsByAttempt[index] = f.issuer.Issue(f.ctx, grant, f.snapshotToken, f.pending)
		}(i)
	}
	close(start)
	wg.Wait()
	if (errorsByAttempt[0] == nil) == (errorsByAttempt[1] == nil) {
		t.Fatalf("competing issuance results = %v; want one success", errorsByAttempt)
	}
	var grants, audits int
	if err := f.database.Get(&grants, `SELECT COUNT(*) FROM exact_task_command_grants WHERE installation_id = ? AND workspace_id = ? AND idempotency_key = ?`, f.grant.InstallationID, f.grant.WorkspaceID, f.grant.IdempotencyKey); err != nil {
		t.Fatal(err)
	}
	if err := f.database.Get(&audits, `SELECT COUNT(*) FROM exact_task_command_audits WHERE idempotency_key = ?`, f.grant.IdempotencyKey); err != nil {
		t.Fatal(err)
	}
	if grants != 1 || audits != 0 {
		t.Fatalf("competing issuance effects: grants=%d audits=%d", grants, audits)
	}
}

type exactIssuerFixture struct {
	ctx           context.Context
	database      *sqlx.DB
	repo          *tasksqlite.Repository
	queue         messagequeue.Repository
	issuer        ExactTaskCommandGrantIssuer
	approval      tasksqlite.ExactTaskCommandApproval
	grant         tasksqlite.ExactTaskCommandGrant
	snapshotToken string
	pending       messagequeue.ExactPendingTransition
}

func newExactIssuerFixture(t *testing.T) *exactIssuerFixture {
	t.Helper()
	ctx := context.Background()
	database, repo := openExactLifecycleRepository(t, filepath.Join(t.TempDir(), "issuer.db"))
	office, err := officesqlite.NewWithDB(database, database, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.RestoreExactTaskTriggersAfterOfficeMigration(); err != nil {
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
	if err := repo.CreateWorkspace(ctx, &taskmodels.Workspace{ID: "workspace", Name: "workspace"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateTask(ctx, &taskmodels.Task{ID: "task", WorkspaceID: "workspace", Title: "Disposable", Description: "before"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateTaskSession(ctx, &taskmodels.TaskSession{ID: "session", TaskID: "task", State: taskmodels.TaskSessionStateCreated}); err != nil {
		t.Fatal(err)
	}
	if err := queue.SetPendingMove(ctx, "session", &messagequeue.PendingMove{TaskID: "task", WorkflowID: "workflow", WorkflowStepID: "step"}); err != nil {
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
		t.Fatalf("pending evidence = %+v, %v", page, err)
	}
	approval := tasksqlite.ExactTaskCommandApproval{InstallationID: "installation", WorkspaceID: "workspace", CapabilityID: "host.v2.write:tasks", ReceiptAuditID: "receipt", Revision: 1}
	if err := repo.UpsertExactTaskCommandApproval(ctx, approval); err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordExactTaskCommandReceipt(ctx, approval, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	issuer, err := NewSQLiteExactTaskCommandGrantIssuer(repo, evidence)
	if err != nil {
		t.Fatal(err)
	}
	return &exactIssuerFixture{ctx: ctx, database: database, repo: repo, queue: queue, issuer: issuer, approval: approval, grant: tasksqlite.ExactTaskCommandGrant{ID: "grant", InstallationID: approval.InstallationID, WorkspaceID: approval.WorkspaceID, TaskID: "task", CapabilityID: approval.CapabilityID, ReceiptAuditID: approval.ReceiptAuditID, ApprovalRevision: approval.Revision, ActionDigest: "marker", IdempotencyKey: "key", ExpiresAt: time.Now().UTC().Add(time.Minute)}, snapshotToken: snapshot.Token, pending: page.PendingTransitions[0]}
}

func (f *exactIssuerFixture) assertNoEffect(t *testing.T) {
	t.Helper()
	var grants, audits int
	if err := f.database.Get(&grants, `SELECT COUNT(*) FROM exact_task_command_grants WHERE id = ?`, f.grant.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.database.Get(&audits, `SELECT COUNT(*) FROM exact_task_command_audits WHERE idempotency_key = ?`, f.grant.IdempotencyKey); err != nil {
		t.Fatal(err)
	}
	task, err := f.repo.GetTask(f.ctx, f.grant.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if grants != 0 || audits != 0 || task.Description != "before" {
		t.Fatalf("denied issuance effects: grants=%d audits=%d task=%+v", grants, audits, task)
	}
}

// The private Host issuer and the command must share the production SQLite
// authority. A fresh repository and Host read are the independent effect proof.
func TestExactCommandComposedTwoWorkspaceLifecycleAfterRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "exact-lifecycle.db")
	database, repo := openExactLifecycleRepository(t, path)
	office, err := officesqlite.NewWithDB(database, database, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.RestoreExactTaskTriggersAfterOfficeMigration(); err != nil {
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
	for _, workspaceID := range []string{"workspace-a", "workspace-b"} {
		if err := repo.CreateWorkspace(ctx, &taskmodels.Workspace{ID: workspaceID, Name: workspaceID}); err != nil {
			t.Fatal(err)
		}
		if err := repo.CreateTask(ctx, &taskmodels.Task{ID: "task-" + workspaceID, WorkspaceID: workspaceID, Title: "Disposable", Description: "before"}); err != nil {
			t.Fatal(err)
		}
		if err := repo.CreateTaskSession(ctx, &taskmodels.TaskSession{ID: "session-" + workspaceID, TaskID: "task-" + workspaceID, State: taskmodels.TaskSessionStateCreated}); err != nil {
			t.Fatal(err)
		}
		if err := queue.SetPendingMove(ctx, "session-"+workspaceID, &messagequeue.PendingMove{TaskID: "task-" + workspaceID, WorkflowID: "workflow", WorkflowStepID: "step"}); err != nil {
			t.Fatal(err)
		}
	}
	authority, err := exactsnapshotauthority.NewSQLite(database)
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := exactsnapshotcomposite.New(authority, office, queue)
	if err != nil {
		t.Fatal(err)
	}
	repo.SetExactTaskCommandCompositeValidator(evidence)
	issuer, err := NewSQLiteExactTaskCommandGrantIssuer(repo, evidence)
	if err != nil {
		t.Fatal(err)
	}
	svc, _, _ := newTestService(t)
	svc.SetDataSources(exactCommandTaskSource{Repository: repo}, nil, nil, nil, nil, nil, nil, nil)
	svc.SetExactTaskDecisionEvidence(evidence)
	svc.SetExactTaskCommandGrantIssuer(issuer)
	bridge, err := NewSQLiteExactTaskCommandApprovalBridge(repo)
	if err != nil {
		t.Fatal(err)
	}
	svc.SetExactTaskCommandApprovalBridge(bridge)
	record, err := svc.Install(ctx, exactTaskAccessPackage(t, "kandev-plugin-exact-lifecycle"))
	if err != nil {
		t.Fatal(err)
	}
	host := svc.hostForPlugin(record.ID).(*pluginHost)
	for _, workspaceID := range []string{"workspace-a", "workspace-b"} {
		if _, err := svc.GrantCapabilityApproval(record.InstallationID, workspaceID, 1, ManifestCapabilityDigest(record.Manifest), []string{"host.v2.read:tasks", "host.v2.write:tasks"}, "human", "disposable", "approval-"+workspaceID); err != nil {
			t.Fatal(err)
		}
	}

	workspaceID := "workspace-a"
	taskID := "task-" + workspaceID
	for _, observedWorkspace := range []string{"workspace-a", "workspace-b"} {
		rows, info, err := host.ListTasksExact(ctx, pluginsdk.ExactTaskQuery{WorkspaceID: observedWorkspace, CapabilityRevision: 1})
		if err != nil || len(rows) != 1 || info.HasMore || info.AuditID == "" {
			t.Fatalf("initial task inventory %s = %+v, %+v, %v", observedWorkspace, rows, info, err)
		}
	}
	before, err := repo.GetTask(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	page, info, err := host.ListTaskDecisionEvidenceExact(ctx, pluginsdk.ExactTaskDecisionEvidenceQuery{WorkspaceID: workspaceID, CapabilityRevision: 1})
	if err != nil || len(page.PendingTransitions) != 1 || info.HasMore {
		t.Fatalf("decision evidence = %+v, %+v, %v", page, info, err)
	}
	grant, err := host.issueExactTaskCommandGrant(ctx, exactTaskCommandGrantRequest{WorkspaceID: workspaceID, TaskID: taskID, CapabilityRevision: 1, DecisionEvidenceSnapshotVersion: info.SnapshotVersion, PendingTransition: page.PendingTransitions[0], Marker: "[exact-marker]", IdempotencyKey: "command-a"})
	if err != nil {
		t.Fatalf("Host grant: %v", err)
	}
	binding, err := host.exactDecisionEvidenceSnapshotBinding(workspaceID, 1, info.SnapshotVersion)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := exactPendingTransitionFromSDK(page.PendingTransitions[0])
	if err != nil {
		t.Fatal(err)
	}
	command := tasksqlite.ExactTaskDescriptionCommand{GrantID: grant.ID, InstallationID: grant.InstallationID, WorkspaceID: grant.WorkspaceID, TaskID: grant.TaskID, CapabilityID: grant.CapabilityID, ReceiptAuditID: grant.ReceiptAuditID, ApprovalRevision: grant.ApprovalRevision, ActionDigest: grant.ActionDigest, IdempotencyKey: grant.IdempotencyKey, Marker: "[exact-marker]", ExpectedResourceVersion: before.ResourceVersion, ExpectedFence: exactCommandWorkspaceFence(t, database, workspaceID), PendingSnapshotToken: binding.ProjectionVersion, PendingTransition: &pending}
	receipt, err := repo.ApplyExactTaskDescriptionCommand(ctx, command)
	if err != nil {
		t.Fatalf("composed command: %v", err)
	}
	if receipt.AuditID != grant.IdempotencyKey || receipt.ResourceVersion != before.ResourceVersion+1 {
		t.Fatalf("receipt = %+v, before version = %d", receipt, before.ResourceVersion)
	}
	otherBeforeRestart, err := repo.GetTask(ctx, "task-workspace-b")
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	restartedDB, restarted := openExactLifecycleRepository(t, path)
	replayed, err := restarted.ApplyExactTaskDescriptionCommand(ctx, command)
	if err != nil || replayed != receipt {
		t.Fatalf("restart command replay = %+v, %v; want %+v", replayed, err, receipt)
	}
	var auditCount int
	if err := restartedDB.Get(&auditCount, `SELECT COUNT(*) FROM exact_task_command_audits WHERE installation_id = ? AND workspace_id = ? AND idempotency_key = ? AND resource_version = ?`, grant.InstallationID, workspaceID, grant.IdempotencyKey, receipt.ResourceVersion); err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 {
		t.Fatalf("restart audit count = %d, want 1", auditCount)
	}
	readHost := &pluginHost{installationID: record.InstallationID, taskData: exactCommandTaskSource{Repository: restarted}, exactSnapshots: newExactSnapshotStore([]byte("restart-read-secret")), exactAuthorize: func(workspace string, revision uint64, capability, _ string) ApprovalDecision {
		if revision != 1 || capability != "host.v2.read:tasks" || workspace != "workspace-a" && workspace != "workspace-b" {
			return ApprovalDecision{}
		}
		return ApprovalDecision{Allowed: true, Receipt: ApprovalReceipt{InstallationID: record.InstallationID, WorkspaceID: workspace, CapabilityID: capability, Revision: revision, AuditID: "read-" + workspace, Result: approvalReceiptAllowed, ObservedAt: time.Now().UTC()}}
	}, exactReadReceipt: func(ApprovalReceipt) error { return nil }}
	for _, check := range []struct {
		workspace, description string
		version                int64
	}{
		{"workspace-a", "[exact-marker]", receipt.ResourceVersion},
		{"workspace-b", "before", otherBeforeRestart.ResourceVersion},
	} {
		rows, info, err := readHost.ListTasksExact(ctx, pluginsdk.ExactTaskQuery{WorkspaceID: check.workspace, CapabilityRevision: 1})
		if err != nil || len(rows) != 1 || info.HasMore || info.AuditID == "" {
			t.Fatalf("restart list %s = %+v, %+v, %v", check.workspace, rows, info, err)
		}
		got, err := readHost.GetTaskExact(ctx, pluginsdk.ExactTaskGetQuery{WorkspaceID: check.workspace, TaskID: "task-" + check.workspace, CapabilityRevision: 1, SnapshotVersion: info.SnapshotVersion})
		if err != nil || got.Description != check.description || got.ResourceVersion != check.version {
			t.Fatalf("restart exact get %s = %+v, %v", check.workspace, got, err)
		}
	}
}

func openExactLifecycleRepository(t *testing.T, path string) (*sqlx.DB, *tasksqlite.Repository) {
	t.Helper()
	raw, err := db.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	database := sqlx.NewDb(raw, "sqlite3")
	t.Cleanup(func() { _ = database.Close() })
	repo, err := tasksqlite.NewWithDB(database, database, nil)
	if err != nil {
		t.Fatal(err)
	}
	return database, repo
}
