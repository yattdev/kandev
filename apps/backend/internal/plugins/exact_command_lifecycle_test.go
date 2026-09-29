package plugins

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/kandev/kandev/internal/common/logger"
	"github.com/kandev/kandev/internal/db"
	"github.com/kandev/kandev/internal/events/bus"
	"github.com/kandev/kandev/internal/exactsnapshotauthority"
	"github.com/kandev/kandev/internal/exactsnapshotcomposite"
	officemodels "github.com/kandev/kandev/internal/office/models"
	officesqlite "github.com/kandev/kandev/internal/office/repository/sqlite"
	"github.com/kandev/kandev/internal/orchestrator/messagequeue"
	taskmodels "github.com/kandev/kandev/internal/task/models"
	tasksqlite "github.com/kandev/kandev/internal/task/repository/sqlite"
	taskservice "github.com/kandev/kandev/internal/task/service"
	v1 "github.com/kandev/kandev/pkg/api/v1"
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
	path          string
	database      *sqlx.DB
	repo          *tasksqlite.Repository
	queue         messagequeue.Repository
	evidence      *exactsnapshotcomposite.Repository
	issuer        ExactTaskCommandGrantIssuer
	approval      tasksqlite.ExactTaskCommandApproval
	grant         tasksqlite.ExactTaskCommandGrant
	snapshotToken string
	pending       messagequeue.ExactPendingTransition
}

func newExactIssuerFixture(t *testing.T) *exactIssuerFixture {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "issuer.db")
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
	return &exactIssuerFixture{ctx: ctx, path: path, database: database, repo: repo, queue: queue, evidence: evidence, issuer: issuer, approval: approval, grant: tasksqlite.ExactTaskCommandGrant{ID: "grant", InstallationID: approval.InstallationID, WorkspaceID: approval.WorkspaceID, TaskID: "task", CapabilityID: approval.CapabilityID, ReceiptAuditID: approval.ReceiptAuditID, ApprovalRevision: approval.Revision, ActionDigest: "marker", IdempotencyKey: "key", ExpiresAt: time.Now().UTC().Add(time.Minute)}, snapshotToken: snapshot.Token, pending: page.PendingTransitions[0]}
}

type exactCommandFailureBus struct {
	bus.EventBus
	failNext  bool
	published int
	eventIDs  []string
}

func (b *exactCommandFailureBus) Publish(ctx context.Context, subject string, event *bus.Event) error {
	if b.failNext {
		b.failNext = false
		return errors.New("injected publication failure")
	}
	if err := b.EventBus.Publish(ctx, subject, event); err != nil {
		return err
	}
	if subject == "task.updated" {
		b.published++
		b.eventIDs = append(b.eventIDs, event.ID)
	}
	return nil
}

func TestPublicExactCommandResumesPostCommitFailuresAfterRestart(t *testing.T) {
	for _, failure := range []string{"publication", "acknowledgement", "delivery_record"} {
		t.Run(failure, func(t *testing.T) {
			f := newExactIssuerFixture(t)
			log := logger.Default()
			events := &exactCommandFailureBus{EventBus: bus.NewMemoryEventBus(log), failNext: failure == "publication"}
			service := taskservice.NewService(taskservice.Repos{Tasks: f.repo, TaskRepos: f.repo, WorkspaceFolders: f.repo, Sessions: f.repo}, events, log, taskservice.RepositoryDiscoveryConfig{})
			issuer, err := NewSQLiteExactTaskCommandGrantIssuerWithTaskUpdatePublisher(f.repo, f.evidence, service)
			if err != nil {
				t.Fatal(err)
			}
			if failure == "acknowledgement" {
				if _, err = f.database.Exec(`CREATE TRIGGER reject_exact_ack BEFORE UPDATE OF published_at ON exact_task_command_outbox WHEN NEW.published_at IS NOT NULL BEGIN SELECT RAISE(ABORT, 'injected acknowledgement failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			if failure == "delivery_record" {
				if _, err = f.database.Exec(`CREATE TRIGGER reject_exact_delivery BEFORE UPDATE OF delivered_at ON exact_task_command_outbox WHEN NEW.delivered_at IS NOT NULL BEGIN SELECT RAISE(ABORT, 'injected delivery record failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			snapshots := newExactSnapshotStore([]byte("public-command-restart-secret"))
			host := &pluginHost{installationID: f.approval.InstallationID, exactSnapshots: snapshots,
				exactAuthorize: func(workspaceID string, revision uint64, capabilityID, _ string) ApprovalDecision {
					if workspaceID != f.approval.WorkspaceID || revision != f.approval.Revision || capabilityID != f.approval.CapabilityID {
						return ApprovalDecision{}
					}
					return ApprovalDecision{Allowed: true, Receipt: ApprovalReceipt{InstallationID: f.approval.InstallationID, WorkspaceID: f.approval.WorkspaceID, CapabilityID: f.approval.CapabilityID, Revision: 1, AuditID: f.approval.ReceiptAuditID, Result: approvalReceiptAllowed, ObservedAt: time.Now().UTC()}}
				},
				exactReadReceipt: func(receipt ApprovalReceipt) error {
					return f.repo.RecordExactTaskCommandReceipt(f.ctx, tasksqlite.ExactTaskCommandApproval{InstallationID: receipt.InstallationID, WorkspaceID: receipt.WorkspaceID, CapabilityID: receipt.CapabilityID, ReceiptAuditID: receipt.AuditID, Revision: receipt.Revision}, receipt.ObservedAt)
				}, exactTaskCommandGrantIssuerDep: func() ExactTaskCommandGrantIssuer { return issuer },
			}
			version, err := snapshots.create(host.exactPageBinding(f.approval.WorkspaceID, 1, "task-decision-evidence", f.snapshotToken), 0)
			if err != nil {
				t.Fatal(err)
			}
			pending := f.pending
			task, err := f.repo.GetTask(f.ctx, f.grant.TaskID)
			if err != nil {
				t.Fatal(err)
			}
			request := pluginsdk.ExactTaskUpdateRequest{WorkspaceID: f.approval.WorkspaceID, TaskID: task.ID, CapabilityRevision: 1, DecisionEvidenceSnapshotVersion: version, PendingTransition: pluginsdk.ExactPendingTaskTransition{SessionID: pending.SessionID, TaskID: pending.TaskID, WorkspaceID: pending.WorkspaceID, SessionIncarnationID: pending.SessionIncarnationID, WorkflowID: pending.WorkflowID, WorkflowStepID: pending.WorkflowStepID, StepPosition: int32(pending.Position), ResourceVersion: pending.ResourceVersion, TaskResourceVersion: pending.TaskResourceVersion, SessionResourceVersion: pending.SessionResourceVersion, QueueGeneration: pending.QueueGeneration, QueuedAt: pending.QueuedAt.UTC().Format(time.RFC3339Nano)}, Marker: "[marker]", IdempotencyKey: "public-failure", ExpectedResourceVersion: task.ResourceVersion}
			first, err := host.UpdateTaskExact(f.ctx, request)
			if err != nil || first.Outcome != pluginsdk.ExactTaskUpdatePending || first.ResourceVersion != task.ResourceVersion+1 {
				t.Fatalf("post-commit %s result = %+v, %v", failure, first, err)
			}
			if failure == "publication" && events.published != 0 || failure != "publication" && events.published != 1 {
				t.Fatalf("published events before restart = %d", events.published)
			}
			if failure == "acknowledgement" {
				if _, err = f.database.Exec(`DROP TRIGGER reject_exact_ack`); err != nil {
					t.Fatal(err)
				}
			}
			if failure == "delivery_record" {
				if _, err = f.database.Exec(`DROP TRIGGER reject_exact_delivery`); err != nil {
					t.Fatal(err)
				}
				if _, err = f.database.Exec(`UPDATE exact_task_command_outbox SET claimed_at = ? WHERE audit_id = ?`, time.Now().UTC().Add(-2*time.Minute), request.IdempotencyKey); err != nil {
					t.Fatal(err)
				}
			}
			if err = f.database.Close(); err != nil {
				t.Fatal(err)
			}
			reopenedDB, reopenedRepo := openExactLifecycleRepository(t, f.path)
			reopenedOffice, err := officesqlite.NewWithDB(reopenedDB, reopenedDB, nil)
			if err != nil {
				t.Fatal(err)
			}
			reopenedQueue, err := messagequeue.NewSQLiteRepository(reopenedDB, reopenedDB)
			if err != nil {
				t.Fatal(err)
			}
			reopenedAuthority, err := exactsnapshotauthority.NewSQLite(reopenedDB)
			if err != nil {
				t.Fatal(err)
			}
			reopenedEvidence, err := exactsnapshotcomposite.New(reopenedAuthority, reopenedOffice, reopenedQueue.(interface {
				messagequeue.ExactPendingTransitionReader
				messagequeue.ExactPendingTransitionAuthorityReader
			}))
			if err != nil {
				t.Fatal(err)
			}
			restartedEvents := &exactCommandFailureBus{EventBus: bus.NewMemoryEventBus(log)}
			if failure == "delivery_record" {
				restartedEvents = events
			}
			restartedService := taskservice.NewService(taskservice.Repos{Tasks: reopenedRepo, TaskRepos: reopenedRepo, WorkspaceFolders: reopenedRepo, Sessions: reopenedRepo}, restartedEvents, log, taskservice.RepositoryDiscoveryConfig{})
			restartedIssuer, err := NewSQLiteExactTaskCommandGrantIssuerWithTaskUpdatePublisher(reopenedRepo, reopenedEvidence, restartedService)
			if err != nil {
				t.Fatal(err)
			}
			restartedHost := &pluginHost{installationID: f.approval.InstallationID, exactSnapshots: newExactSnapshotStore([]byte("new-connection-secret")), exactAuthorize: host.exactAuthorize, exactTaskCommandGrantIssuerDep: func() ExactTaskCommandGrantIssuer { return restartedIssuer }}
			replayed, err := restartedHost.UpdateTaskExact(f.ctx, request)
			wantOutcome := pluginsdk.ExactTaskUpdateDurable
			if failure == "delivery_record" {
				wantOutcome = pluginsdk.ExactTaskUpdatePending
			}
			if err != nil || replayed.Outcome != wantOutcome || replayed.AuditID != first.AuditID || replayed.ResourceVersion != first.ResourceVersion {
				t.Fatalf("restarted %s replay = %+v, %v", failure, replayed, err)
			}
			wantPublished := 1
			if failure == "acknowledgement" {
				wantPublished = 0
			}
			if restartedEvents.published != wantPublished {
				t.Fatalf("restarted publications = %d, want %d", restartedEvents.published, wantPublished)
			}
			if failure == "delivery_record" && (len(restartedEvents.eventIDs) != 1 || restartedEvents.eventIDs[0] == "") {
				t.Fatalf("delivery-record recovery event identities = %v, want one stable event identity", restartedEvents.eventIDs)
			}
			stored, err := reopenedRepo.GetTask(f.ctx, task.ID)
			if err != nil || stored.Description != "before\n\n[marker]" || stored.ResourceVersion != first.ResourceVersion {
				t.Fatalf("reopened task = %+v, %v", stored, err)
			}
			var audits int
			if err = reopenedRepo.DB().QueryRowContext(f.ctx, `SELECT COUNT(*) FROM exact_task_command_audits WHERE idempotency_key = ?`, request.IdempotencyKey).Scan(&audits); err != nil || audits != 1 {
				t.Fatalf("reopened audit count = %d, %v", audits, err)
			}
			var publishedAt any
			if err = reopenedRepo.DB().QueryRowContext(f.ctx, `SELECT published_at FROM exact_task_command_outbox WHERE audit_id = ?`, request.IdempotencyKey).Scan(&publishedAt); err != nil {
				t.Fatalf("reopened outbox acknowledgement read = %v", err)
			}
			if failure != "delivery_record" && publishedAt == nil || failure == "delivery_record" && publishedAt != nil {
				t.Fatalf("reopened %s outbox acknowledgement = %v", failure, publishedAt)
			}
			changed := request
			changed.Marker = "[changed]"
			if _, err = restartedHost.UpdateTaskExact(f.ctx, changed); err == nil {
				t.Fatal("changed replay succeeded")
			}
			stored, err = reopenedRepo.GetTask(f.ctx, task.ID)
			if err != nil || stored.Description != "before\n\n[marker]" || stored.ResourceVersion != first.ResourceVersion {
				t.Fatalf("changed replay altered reopened task = %+v, %v", stored, err)
			}
		})
	}
}

func TestExactCommandCompositeBridgeMintsAndConsumesGrantAtomically(t *testing.T) {
	f := newExactIssuerFixture(t)
	task, err := f.repo.GetTask(f.ctx, f.grant.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	marker := "[marker]"
	receipt, err := f.issuer.Execute(f.ctx, f.grant, f.snapshotToken, f.pending, marker, task.ResourceVersion, "")
	if err != nil {
		t.Fatal(err)
	}
	if receipt.AuditID != f.grant.IdempotencyKey {
		t.Fatalf("receipt = %+v", receipt)
	}
	stored, err := f.repo.GetTask(f.ctx, f.grant.TaskID)
	if err != nil || stored.Description != "before\n\n"+marker || stored.ResourceVersion != receipt.ResourceVersion {
		t.Fatalf("stored task = %+v, %v", stored, err)
	}
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
		primaryID := "task-" + workspaceID
		if err := repo.CreateTask(ctx, &taskmodels.Task{ID: primaryID, WorkspaceID: workspaceID, Title: "Disposable", Description: "before", State: v1.TaskStateTODO}); err != nil {
			t.Fatal(err)
		}
		blockedID := "blocked-" + workspaceID
		if err := repo.CreateTask(ctx, &taskmodels.Task{ID: blockedID, WorkspaceID: workspaceID, Title: "Blocked", State: v1.TaskStateBlocked}); err != nil {
			t.Fatal(err)
		}
		if err := repo.CreateTask(ctx, &taskmodels.Task{ID: "done-" + workspaceID, WorkspaceID: workspaceID, Title: "Done", State: v1.TaskStateCompleted}); err != nil {
			t.Fatal(err)
		}
		if err := office.CreateTaskBlocker(ctx, &officemodels.TaskBlocker{TaskID: blockedID, BlockerTaskID: primaryID}); err != nil {
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
		rows, info, err := host.ListTasksExact(ctx, pluginsdk.ExactTaskQuery{WorkspaceID: observedWorkspace, CapabilityRevision: 1, Page: pluginsdk.ExactPage{Limit: 2}})
		if err != nil || len(rows) != 2 || !info.HasMore || info.NextCursor == "" || info.SnapshotVersion == "" {
			t.Fatalf("initial task page %s = %+v, %+v, %v", observedWorkspace, rows, info, err)
		}
		states := map[string]string{}
		for _, row := range rows {
			states[row.ID] = row.State
		}
		for info.HasMore {
			rows, info, err = host.ListTasksExact(ctx, pluginsdk.ExactTaskQuery{WorkspaceID: observedWorkspace, CapabilityRevision: 1, Page: pluginsdk.ExactPage{Limit: 2, Cursor: info.NextCursor, SnapshotVersion: info.SnapshotVersion}})
			if err != nil {
				t.Fatalf("next task page %s: %v", observedWorkspace, err)
			}
			for _, row := range rows {
				states[row.ID] = row.State
			}
		}
		if len(states) != 3 || states["blocked-"+observedWorkspace] != string(v1.TaskStateBlocked) || states["done-"+observedWorkspace] != string(v1.TaskStateCompleted) || states["task-"+observedWorkspace] != string(v1.TaskStateTODO) || info.AuditID == "" {
			t.Fatalf("exhausted task inventory %s = %#v; final page %+v", observedWorkspace, states, info)
		}
	}
	before, err := repo.GetTask(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	page, info, err := host.ListTaskDecisionEvidenceExact(ctx, pluginsdk.ExactTaskDecisionEvidenceQuery{WorkspaceID: workspaceID, CapabilityRevision: 1})
	if err != nil || len(page.PendingTransitions) != 1 || len(page.Relations) != 1 || info.HasMore {
		t.Fatalf("decision evidence = %+v, %+v, %v", page, info, err)
	}
	relation := page.Relations[0]
	blocked, err := repo.GetTask(ctx, "blocked-"+workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if relation.WorkspaceID != workspaceID || relation.TaskID != blocked.ID || relation.BlockerTaskID != taskID || relation.TaskResourceVersion != blocked.ResourceVersion || relation.BlockerResourceVersion != before.ResourceVersion || relation.ResourceVersion <= 0 {
		t.Fatalf("complete blocker relation = %+v, blocked=%+v, blocker=%+v", relation, blocked, before)
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
		{"workspace-a", "before\n\n[exact-marker]", receipt.ResourceVersion},
		{"workspace-b", "before", otherBeforeRestart.ResourceVersion},
	} {
		rows, info, err := readHost.ListTasksExact(ctx, pluginsdk.ExactTaskQuery{WorkspaceID: check.workspace, CapabilityRevision: 1})
		if err != nil || len(rows) != 3 || info.HasMore || info.AuditID == "" {
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
