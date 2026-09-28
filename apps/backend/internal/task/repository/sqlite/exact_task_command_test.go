package sqlite

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/exactsnapshotauthority"
	"github.com/kandev/kandev/internal/orchestrator/messagequeue"
	"github.com/kandev/kandev/internal/task/models"
)

type exactTaskCommandQueue interface {
	messagequeue.Repository
	messagequeue.ExactPendingTransitionReader
	messagequeue.ExactPendingTransitionAuthorityReader
}

func TestIssueExactTaskCommandGrantInAuthorityTxBindsPendingEvidenceAndCommit(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForArchiveTests(t, "exact-grant-authority")
	queueRepo, err := messagequeue.NewSQLiteRepository(repo.db, repo.db)
	if err != nil {
		t.Fatal(err)
	}
	queue, ok := queueRepo.(exactTaskCommandQueue)
	if !ok {
		t.Fatalf("queue does not provide exact authority: %T", queueRepo)
	}
	repo.SetExactTaskCommandPendingValidator(queue)

	task, err := repo.GetTask(ctx, "exact-grant-authority")
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.CreateTaskSession(ctx, &models.TaskSession{ID: "grant-session", TaskID: task.ID, State: models.TaskSessionStateCreated}); err != nil {
		t.Fatal(err)
	}
	if err = queue.SetPendingMove(ctx, "grant-session", &messagequeue.PendingMove{TaskID: task.ID, WorkflowID: "workflow", WorkflowStepID: "step"}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := queue.OpenExactPendingTransitionSnapshot(ctx, messagequeue.ExactPendingTransitionSnapshotRequest{WorkspaceID: task.WorkspaceID})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := queue.PageExactPendingTransitionSnapshot(ctx, snapshot.Token, 0, 1)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending evidence = %#v, %v", pending, err)
	}
	approval := ExactTaskCommandApproval{InstallationID: "installation", WorkspaceID: task.WorkspaceID, CapabilityID: exactTaskDescriptionCommandCapability, ReceiptAuditID: "receipt", Revision: 1}
	if err = repo.UpsertExactTaskCommandApproval(ctx, approval); err != nil {
		t.Fatal(err)
	}
	if err = repo.RecordExactTaskCommandReceipt(ctx, approval, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	grant := ExactTaskCommandGrant{ID: "grant", InstallationID: approval.InstallationID, WorkspaceID: task.WorkspaceID, TaskID: task.ID, CapabilityID: approval.CapabilityID, ReceiptAuditID: approval.ReceiptAuditID, ApprovalRevision: approval.Revision, ActionDigest: "marker-v1", IdempotencyKey: "command", ExpiresAt: time.Now().UTC().Add(time.Minute)}
	authority, err := exactsnapshotauthority.NewSQLite(repo.db)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := queue.BeginExactPendingTransitionSnapshotAuthorityTx(ctx, authority)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()

	if err = repo.issueExactTaskCommandGrantInAuthorityTx(ctx, authority, tx, grant, snapshot.Token, pending[0]); err != nil {
		t.Fatalf("issue grant in authority transaction: %v", err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = repo.db.GetContext(ctx, &count, repo.db.Rebind(`SELECT COUNT(*) FROM exact_task_command_grants WHERE id = ?`), grant.ID); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("grant count = %d, want 1", count)
	}
}

func TestIssueExactTaskCommandGrantInAuthorityTxRejectsChangedEvidenceWithoutEffect(t *testing.T) {
	for name, mutate := range map[string]func(t *testing.T, fixture *exactTaskGrantAuthorityFixture){
		"stale pending row": func(t *testing.T, fixture *exactTaskGrantAuthorityFixture) {
			t.Helper()
			if _, err := fixture.queue.TakePendingMove(fixture.ctx, fixture.pending.SessionID); err != nil {
				t.Fatal(err)
			}
		},
		"foreign row": func(t *testing.T, fixture *exactTaskGrantAuthorityFixture) {
			t.Helper()
			fixture.pending.WorkspaceID = "other-workspace"
		},
		"expired snapshot": func(t *testing.T, fixture *exactTaskGrantAuthorityFixture) {
			t.Helper()
			if _, err := fixture.repo.db.Exec(fixture.repo.db.Rebind(`UPDATE exact_pending_snapshots SET expires_at = ? WHERE token = ?`), time.Now().UTC().Add(-time.Minute), fixture.snapshotToken); err != nil {
				t.Fatal(err)
			}
		},
		"revoked approval": func(t *testing.T, fixture *exactTaskGrantAuthorityFixture) {
			t.Helper()
			if err := fixture.repo.RevokeExactTaskCommandApproval(fixture.ctx, fixture.approval); err != nil {
				t.Fatal(err)
			}
		},
		"expired grant": func(t *testing.T, fixture *exactTaskGrantAuthorityFixture) {
			t.Helper()
			fixture.grant.ExpiresAt = time.Now().UTC().Add(-time.Minute)
		},
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newExactTaskGrantAuthorityFixture(t)
			mutate(t, fixture)
			tx, err := fixture.queue.BeginExactPendingTransitionSnapshotAuthorityTx(fixture.ctx, fixture.authority)
			if err != nil {
				t.Fatal(err)
			}
			if err = fixture.repo.issueExactTaskCommandGrantInAuthorityTx(fixture.ctx, fixture.authority, tx, fixture.grant, fixture.snapshotToken, fixture.pending); !errors.Is(err, ErrExactTaskCommandUnavailable) {
				t.Fatalf("issue changed evidence = %v", err)
			}
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			assertExactTaskGrantAuthorityNoEffect(t, fixture)
		})
	}
}

func TestIssueExactTaskCommandGrantInAuthorityTxRollsBackGrant(t *testing.T) {
	fixture := newExactTaskGrantAuthorityFixture(t)
	tx, err := fixture.queue.BeginExactPendingTransitionSnapshotAuthorityTx(fixture.ctx, fixture.authority)
	if err != nil {
		t.Fatal(err)
	}
	if err = fixture.repo.issueExactTaskCommandGrantInAuthorityTx(fixture.ctx, fixture.authority, tx, fixture.grant, fixture.snapshotToken, fixture.pending); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	assertExactTaskGrantAuthorityNoEffect(t, fixture)
}

type exactTaskGrantAuthorityFixture struct {
	ctx           context.Context
	repo          *Repository
	queue         exactTaskCommandQueue
	authority     *exactsnapshotauthority.Authority
	approval      ExactTaskCommandApproval
	grant         ExactTaskCommandGrant
	snapshotToken string
	pending       messagequeue.ExactPendingTransition
}

func newExactTaskGrantAuthorityFixture(t *testing.T) *exactTaskGrantAuthorityFixture {
	t.Helper()
	ctx := context.Background()
	repo := newRepoForArchiveTests(t, "exact-grant-authority")
	queueRepo, err := messagequeue.NewSQLiteRepository(repo.db, repo.db)
	if err != nil {
		t.Fatal(err)
	}
	queue, ok := queueRepo.(exactTaskCommandQueue)
	if !ok {
		t.Fatalf("queue does not provide exact authority: %T", queueRepo)
	}
	repo.SetExactTaskCommandPendingValidator(queue)
	task, err := repo.GetTask(ctx, "exact-grant-authority")
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.CreateTaskSession(ctx, &models.TaskSession{ID: "grant-session", TaskID: task.ID, State: models.TaskSessionStateCreated}); err != nil {
		t.Fatal(err)
	}
	if err = queue.SetPendingMove(ctx, "grant-session", &messagequeue.PendingMove{TaskID: task.ID, WorkflowID: "workflow", WorkflowStepID: "step"}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := queue.OpenExactPendingTransitionSnapshot(ctx, messagequeue.ExactPendingTransitionSnapshotRequest{WorkspaceID: task.WorkspaceID})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := queue.PageExactPendingTransitionSnapshot(ctx, snapshot.Token, 0, 1)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending evidence = %#v, %v", pending, err)
	}
	approval := ExactTaskCommandApproval{InstallationID: "installation", WorkspaceID: task.WorkspaceID, CapabilityID: exactTaskDescriptionCommandCapability, ReceiptAuditID: "receipt", Revision: 1}
	if err = repo.UpsertExactTaskCommandApproval(ctx, approval); err != nil {
		t.Fatal(err)
	}
	if err = repo.RecordExactTaskCommandReceipt(ctx, approval, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	authority, err := exactsnapshotauthority.NewSQLite(repo.db)
	if err != nil {
		t.Fatal(err)
	}
	return &exactTaskGrantAuthorityFixture{ctx: ctx, repo: repo, queue: queue, authority: authority, approval: approval, grant: ExactTaskCommandGrant{ID: "grant", InstallationID: approval.InstallationID, WorkspaceID: task.WorkspaceID, TaskID: task.ID, CapabilityID: approval.CapabilityID, ReceiptAuditID: approval.ReceiptAuditID, ApprovalRevision: approval.Revision, ActionDigest: "marker-v1", IdempotencyKey: "command", ExpiresAt: time.Now().UTC().Add(time.Minute)}, snapshotToken: snapshot.Token, pending: pending[0]}
}

func assertExactTaskGrantAuthorityNoEffect(t *testing.T, fixture *exactTaskGrantAuthorityFixture) {
	t.Helper()
	var grants, audits int
	if err := fixture.repo.db.Get(&grants, fixture.repo.db.Rebind(`SELECT COUNT(*) FROM exact_task_command_grants WHERE id = ?`), fixture.grant.ID); err != nil {
		t.Fatal(err)
	}
	if err := fixture.repo.db.Get(&audits, fixture.repo.db.Rebind(`SELECT COUNT(*) FROM exact_task_command_audits WHERE idempotency_key = ?`), fixture.grant.IdempotencyKey); err != nil {
		t.Fatal(err)
	}
	task, err := fixture.repo.GetTask(fixture.ctx, fixture.grant.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if grants != 0 || audits != 0 || task.Description != "" {
		t.Fatalf("unexpected authority issuance effect: grants=%d audits=%d task=%+v", grants, audits, task)
	}
}

func TestExactTaskCommandConsumesGrantAndPersistsReceipt(t *testing.T) {
	repo := newRepoForArchiveTests(t, "exact-command-task")
	ctx := context.Background()
	command := prepareExactTaskCommand(t, repo, "exact-command-task", "grant-one", "key-one")
	receipt, err := repo.ApplyExactTaskDescriptionCommand(ctx, command)
	if err != nil {
		t.Fatalf("ApplyExactTaskDescriptionCommand: %v", err)
	}
	if receipt.AuditID != command.IdempotencyKey || receipt.ResourceVersion != command.ExpectedResourceVersion+1 {
		t.Fatalf("receipt = %+v", receipt)
	}
	task, err := repo.GetTask(ctx, command.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Description != command.Marker {
		t.Fatalf("description = %q", task.Description)
	}
	if _, err = repo.ApplyExactTaskDescriptionCommand(ctx, command); err != nil {
		t.Fatalf("same idempotency replay: %v", err)
	}
	changed := command
	changed.ActionDigest = "changed"
	if _, err = repo.ApplyExactTaskDescriptionCommand(ctx, changed); !errors.Is(err, ErrExactTaskCommandUnavailable) {
		t.Fatalf("changed replay = %v", err)
	}
	for name, mutate := range map[string]func(*ExactTaskDescriptionCommand){
		"target": func(competing *ExactTaskDescriptionCommand) { competing.TaskID = "other-task" },
		"grant":  func(competing *ExactTaskDescriptionCommand) { competing.GrantID = "other-grant" },
		"marker": func(competing *ExactTaskDescriptionCommand) { competing.Marker = "[other-marker]" },
	} {
		t.Run(name, func(t *testing.T) {
			competing := command
			mutate(&competing)
			if _, err = repo.ApplyExactTaskDescriptionCommand(ctx, competing); !errors.Is(err, ErrExactTaskCommandUnavailable) {
				t.Fatalf("competing replay = %v", err)
			}
		})
	}
	var auditCount int
	if err = repo.db.QueryRow(repo.db.Rebind(`SELECT COUNT(*) FROM exact_task_command_audits WHERE idempotency_key = ?`), command.IdempotencyKey).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 {
		t.Fatalf("audit count after competing replays = %d, want 1", auditCount)
	}
	task, err = repo.GetTask(ctx, command.TaskID)
	if err != nil || task.Description != command.Marker || task.ResourceVersion != receipt.ResourceVersion {
		t.Fatalf("task after competing replays = %+v, %v", task, err)
	}
}

func TestExactTaskCommandRejectsRevokedAndStaleVersionWithoutEffect(t *testing.T) {
	repo := newRepoForArchiveTests(t, "exact-command-revoked", "exact-command-stale")
	ctx := context.Background()
	revoked := prepareExactTaskCommand(t, repo, "exact-command-revoked", "grant-revoked", "key-revoked")
	if err := repo.RevokeExactTaskCommandApproval(ctx, ExactTaskCommandApproval{InstallationID: revoked.InstallationID, WorkspaceID: revoked.WorkspaceID, CapabilityID: revoked.CapabilityID, ReceiptAuditID: "revoke-receipt", Revision: revoked.ApprovalRevision}); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, err := repo.ApplyExactTaskDescriptionCommand(ctx, revoked); !errors.Is(err, ErrExactTaskCommandUnavailable) {
		t.Fatalf("revoked command = %v", err)
	}
	stale := prepareExactTaskCommand(t, repo, "exact-command-stale", "grant-stale", "key-stale")
	stale.ExpectedResourceVersion++
	if _, err := repo.ApplyExactTaskDescriptionCommand(ctx, stale); !errors.Is(err, ErrExactTaskCommandUnavailable) {
		t.Fatalf("stale command = %v", err)
	}
	var consumedAt *time.Time
	if err := repo.db.QueryRow(`SELECT consumed_at FROM exact_task_command_grants WHERE id = ?`, stale.GrantID).Scan(&consumedAt); err != nil {
		t.Fatal(err)
	}
	if consumedAt != nil {
		t.Fatal("stale command consumed grant")
	}
}

func TestExactTaskCommandRejectsExpiredGrantAfterRestart(t *testing.T) {
	repo := newRepoForArchiveTests(t, "exact-command-expired")
	ctx := context.Background()
	command := prepareExactTaskCommand(t, repo, "exact-command-expired", "grant-expired", "key-expired")
	if _, err := repo.db.Exec(repo.db.Rebind(`UPDATE exact_task_command_grants SET expires_at = ? WHERE id = ?`), time.Now().UTC().Add(-time.Minute), command.GrantID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ApplyExactTaskDescriptionCommand(ctx, command); !errors.Is(err, ErrExactTaskCommandUnavailable) {
		t.Fatalf("expired command = %v", err)
	}
}

func TestExactTaskCommandRollsBackMarkerAndGrantOnAuditFailure(t *testing.T) {
	repo := newRepoForArchiveTests(t, "exact-command-rollback")
	command := prepareExactTaskCommand(t, repo, "exact-command-rollback", "grant-rollback", "key-rollback")
	repo.exactTaskCommandBeforeAudit = func() error { return errors.New("audit unavailable") }
	if _, err := repo.ApplyExactTaskDescriptionCommand(context.Background(), command); err == nil {
		t.Fatal("command unexpectedly succeeded")
	}
	task, err := repo.GetTask(context.Background(), command.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Description == command.Marker || task.ResourceVersion != command.ExpectedResourceVersion {
		t.Fatalf("task changed after rollback: %+v", task)
	}
	var consumedAt *time.Time
	if err := repo.db.QueryRow(repo.db.Rebind(`SELECT consumed_at FROM exact_task_command_grants WHERE id = ?`), command.GrantID).Scan(&consumedAt); err != nil {
		t.Fatal(err)
	}
	if consumedAt != nil {
		t.Fatal("rollback consumed grant")
	}
}

func TestExactTaskCommandConcurrentRevokeAndWriteHasAtMostOneEffect(t *testing.T) {
	repo := newRepoForArchiveTests(t, "exact-command-race")
	command := prepareExactTaskCommand(t, repo, "exact-command-race", "grant-race", "key-race")
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = repo.ApplyExactTaskDescriptionCommand(context.Background(), command) }()
	go func() {
		defer wg.Done()
		_ = repo.RevokeExactTaskCommandApproval(context.Background(), ExactTaskCommandApproval{InstallationID: command.InstallationID, WorkspaceID: command.WorkspaceID, CapabilityID: command.CapabilityID, ReceiptAuditID: "revoke-race", Revision: command.ApprovalRevision})
	}()
	wg.Wait()
	var count int
	if err := repo.db.QueryRow(repo.db.Rebind(`SELECT COUNT(*) FROM exact_task_command_audits WHERE idempotency_key = ?`), command.IdempotencyKey).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count > 1 {
		t.Fatalf("audit count = %d", count)
	}
	_, err := repo.ApplyExactTaskDescriptionCommand(context.Background(), command)
	if count == 1 && err != nil {
		t.Fatalf("completed idempotency replay = %v", err)
	}
	if count == 0 && !errors.Is(err, ErrExactTaskCommandUnavailable) {
		t.Fatalf("revoked replay = %v", err)
	}
}

func prepareExactTaskCommand(t *testing.T, repo *Repository, taskID, grantID, key string) ExactTaskDescriptionCommand {
	t.Helper()
	ctx := context.Background()
	task, err := repo.GetTask(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	var fence int64
	if err = repo.db.QueryRow(repo.db.Rebind(`SELECT revision FROM exact_task_workspace_fences WHERE workspace_id = ?`), task.WorkspaceID).Scan(&fence); err != nil {
		t.Fatal(err)
	}
	approval := ExactTaskCommandApproval{InstallationID: "install-" + grantID, WorkspaceID: task.WorkspaceID, CapabilityID: "host.v2.write:tasks", ReceiptAuditID: "receipt-" + grantID, Revision: 1}
	if err = repo.UpsertExactTaskCommandApproval(ctx, approval); err != nil {
		t.Fatal(err)
	}
	if err = repo.RecordExactTaskCommandReceipt(ctx, approval, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	grant := ExactTaskCommandGrant{ID: grantID, InstallationID: approval.InstallationID, WorkspaceID: approval.WorkspaceID, TaskID: taskID, CapabilityID: approval.CapabilityID, ReceiptAuditID: approval.ReceiptAuditID, ApprovalRevision: approval.Revision, ActionDigest: "digest-" + grantID, IdempotencyKey: key, ExpiresAt: time.Now().UTC().Add(time.Minute)}
	if err = repo.IssueExactTaskCommandGrant(ctx, grant); err != nil {
		t.Fatal(err)
	}
	return ExactTaskDescriptionCommand{GrantID: grant.ID, InstallationID: grant.InstallationID, WorkspaceID: grant.WorkspaceID, TaskID: grant.TaskID, CapabilityID: grant.CapabilityID, ReceiptAuditID: grant.ReceiptAuditID, ApprovalRevision: grant.ApprovalRevision, ActionDigest: grant.ActionDigest, IdempotencyKey: grant.IdempotencyKey, Marker: "[exact-marker]", ExpectedResourceVersion: task.ResourceVersion, ExpectedFence: fence}
}

var _ = models.Task{}
