package sqlite

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/task/models"
)

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
