package sqlite_test

import (
	"context"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/kandev/kandev/internal/db"
	"github.com/kandev/kandev/internal/orchestrator/messagequeue"
	"github.com/kandev/kandev/internal/task/models"
	tasksqlite "github.com/kandev/kandev/internal/task/repository/sqlite"
)

type exactCommandQueue interface {
	messagequeue.Repository
	messagequeue.ExactPendingTransitionReader
	messagequeue.ExactPendingTransitionAuthorityReader
}

// TestExactTaskCommandAuthorityValidatorReplaysCompletedCommandAfterEvidenceFenceChanges
// proves an exact completed command remains idempotent even though its own task
// write invalidates the pending-transition evidence used to authorize it.
func TestExactTaskCommandAuthorityValidatorReplaysCompletedCommandAfterEvidenceFenceChanges(t *testing.T) {
	ctx := context.Background()
	database, err := db.OpenSQLite(t.TempDir() + "/exact-command-authority.db")
	if err != nil {
		t.Fatal(err)
	}
	sqliteDB := sqlx.NewDb(database, "sqlite3")
	t.Cleanup(func() { _ = sqliteDB.Close() })

	repo, err := tasksqlite.NewWithDB(sqliteDB, sqliteDB, nil)
	if err != nil {
		t.Fatal(err)
	}
	queueRepository, err := messagequeue.NewSQLiteRepository(sqliteDB, sqliteDB)
	if err != nil {
		t.Fatal(err)
	}
	queue, ok := queueRepository.(exactCommandQueue)
	if !ok {
		t.Fatalf("queue does not provide exact authority: %T", queueRepository)
	}
	repo.SetExactTaskCommandPendingValidator(queue)

	if err = repo.CreateWorkspace(ctx, &models.Workspace{ID: "workspace", Name: "Workspace"}); err != nil {
		t.Fatal(err)
	}
	if err = repo.CreateTask(ctx, &models.Task{ID: "task", WorkspaceID: "workspace", Title: "Task", Description: "before"}); err != nil {
		t.Fatal(err)
	}
	if err = repo.CreateTaskSession(ctx, &models.TaskSession{ID: "session", TaskID: "task", State: models.TaskSessionStateCreated}); err != nil {
		t.Fatal(err)
	}
	if err = queue.SetPendingMove(ctx, "session", &messagequeue.PendingMove{TaskID: "task", WorkflowID: "workflow", WorkflowStepID: "step"}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := queue.OpenExactPendingTransitionSnapshot(ctx, messagequeue.ExactPendingTransitionSnapshotRequest{WorkspaceID: "workspace"})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := queue.PageExactPendingTransitionSnapshot(ctx, snapshot.Token, 0, 1)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending evidence = %#v, %v", pending, err)
	}
	task, err := repo.GetTask(ctx, "task")
	if err != nil {
		t.Fatal(err)
	}
	fence, err := repo.ExactTaskCommandWorkspaceFence(ctx, "workspace")
	if err != nil {
		t.Fatal(err)
	}
	approval := tasksqlite.ExactTaskCommandApproval{InstallationID: "installation", WorkspaceID: "workspace", CapabilityID: "host.v2.write:tasks", ReceiptAuditID: "receipt", Revision: 1}
	if err = repo.UpsertExactTaskCommandApproval(ctx, approval); err != nil {
		t.Fatal(err)
	}
	if err = repo.RecordExactTaskCommandReceipt(ctx, approval, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	grant := tasksqlite.ExactTaskCommandGrant{ID: "grant", InstallationID: approval.InstallationID, WorkspaceID: approval.WorkspaceID, TaskID: task.ID, CapabilityID: approval.CapabilityID, ReceiptAuditID: approval.ReceiptAuditID, ApprovalRevision: approval.Revision, ActionDigest: "marker-v1", IdempotencyKey: "command", ExpiresAt: time.Now().UTC().Add(time.Minute)}
	if err = repo.IssueExactTaskCommandGrant(ctx, grant); err != nil {
		t.Fatal(err)
	}
	command := tasksqlite.ExactTaskDescriptionCommand{GrantID: grant.ID, InstallationID: grant.InstallationID, WorkspaceID: grant.WorkspaceID, TaskID: grant.TaskID, CapabilityID: grant.CapabilityID, ReceiptAuditID: grant.ReceiptAuditID, ApprovalRevision: grant.ApprovalRevision, ActionDigest: grant.ActionDigest, IdempotencyKey: grant.IdempotencyKey, Marker: "[exact-marker]", ExpectedResourceVersion: task.ResourceVersion, ExpectedFence: fence, PendingSnapshotToken: snapshot.Token, PendingTransition: &pending[0]}

	first, err := repo.ApplyExactTaskDescriptionCommand(ctx, command)
	if err != nil {
		t.Fatalf("first command: %v", err)
	}
	second, err := repo.ApplyExactTaskDescriptionCommand(ctx, command)
	if err != nil {
		t.Fatalf("completed command replay: %v", err)
	}
	if second != first {
		t.Fatalf("replay receipt = %+v, want %+v", second, first)
	}
}

// TestExactTaskCommandAuthorityValidatorRejectsForeignEvidenceWithoutEffect
// proves a command cannot validate a pending transition from another workspace.
func TestExactTaskCommandAuthorityValidatorRejectsForeignEvidenceWithoutEffect(t *testing.T) {
	ctx := context.Background()
	database, err := db.OpenSQLite(t.TempDir() + "/exact-command-foreign.db")
	if err != nil {
		t.Fatal(err)
	}
	sqliteDB := sqlx.NewDb(database, "sqlite3")
	t.Cleanup(func() { _ = sqliteDB.Close() })
	repo, err := tasksqlite.NewWithDB(sqliteDB, sqliteDB, nil)
	if err != nil {
		t.Fatal(err)
	}
	queueRepository, err := messagequeue.NewSQLiteRepository(sqliteDB, sqliteDB)
	if err != nil {
		t.Fatal(err)
	}
	queue := queueRepository.(exactCommandQueue)
	repo.SetExactTaskCommandPendingValidator(queue)

	for _, workspace := range []string{"workspace", "other-workspace"} {
		if err = repo.CreateWorkspace(ctx, &models.Workspace{ID: workspace, Name: workspace}); err != nil {
			t.Fatal(err)
		}
	}
	if err = repo.CreateTask(ctx, &models.Task{ID: "task", WorkspaceID: "workspace", Title: "Task", Description: "before"}); err != nil {
		t.Fatal(err)
	}
	if err = repo.CreateTask(ctx, &models.Task{ID: "other-task", WorkspaceID: "other-workspace", Title: "Other task"}); err != nil {
		t.Fatal(err)
	}
	if err = repo.CreateTaskSession(ctx, &models.TaskSession{ID: "other-session", TaskID: "other-task", State: models.TaskSessionStateCreated}); err != nil {
		t.Fatal(err)
	}
	if err = queue.SetPendingMove(ctx, "other-session", &messagequeue.PendingMove{TaskID: "other-task", WorkflowID: "workflow", WorkflowStepID: "step"}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := queue.OpenExactPendingTransitionSnapshot(ctx, messagequeue.ExactPendingTransitionSnapshotRequest{WorkspaceID: "other-workspace"})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := queue.PageExactPendingTransitionSnapshot(ctx, snapshot.Token, 0, 1)
	if err != nil || len(pending) != 1 {
		t.Fatalf("foreign pending evidence = %#v, %v", pending, err)
	}
	task, err := repo.GetTask(ctx, "task")
	if err != nil {
		t.Fatal(err)
	}
	fence, err := repo.ExactTaskCommandWorkspaceFence(ctx, "workspace")
	if err != nil {
		t.Fatal(err)
	}
	approval := tasksqlite.ExactTaskCommandApproval{InstallationID: "installation", WorkspaceID: "workspace", CapabilityID: "host.v2.write:tasks", ReceiptAuditID: "receipt", Revision: 1}
	if err = repo.UpsertExactTaskCommandApproval(ctx, approval); err != nil {
		t.Fatal(err)
	}
	if err = repo.RecordExactTaskCommandReceipt(ctx, approval, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	grant := tasksqlite.ExactTaskCommandGrant{ID: "grant", InstallationID: approval.InstallationID, WorkspaceID: approval.WorkspaceID, TaskID: task.ID, CapabilityID: approval.CapabilityID, ReceiptAuditID: approval.ReceiptAuditID, ApprovalRevision: approval.Revision, ActionDigest: "marker-v1", IdempotencyKey: "command", ExpiresAt: time.Now().UTC().Add(time.Minute)}
	if err = repo.IssueExactTaskCommandGrant(ctx, grant); err != nil {
		t.Fatal(err)
	}
	command := tasksqlite.ExactTaskDescriptionCommand{GrantID: grant.ID, InstallationID: grant.InstallationID, WorkspaceID: grant.WorkspaceID, TaskID: grant.TaskID, CapabilityID: grant.CapabilityID, ReceiptAuditID: grant.ReceiptAuditID, ApprovalRevision: grant.ApprovalRevision, ActionDigest: grant.ActionDigest, IdempotencyKey: grant.IdempotencyKey, Marker: "[exact-marker]", ExpectedResourceVersion: task.ResourceVersion, ExpectedFence: fence, PendingSnapshotToken: snapshot.Token, PendingTransition: &pending[0]}
	if _, err = repo.ApplyExactTaskDescriptionCommand(ctx, command); err == nil {
		t.Fatal("foreign pending evidence command unexpectedly succeeded")
	}
	stored, err := repo.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Description != "before" || stored.ResourceVersion != task.ResourceVersion {
		t.Fatalf("foreign evidence changed task: %+v", stored)
	}
	var consumed, audits int
	if err = sqliteDB.Get(&consumed, `SELECT COUNT(*) FROM exact_task_command_grants WHERE id = ? AND consumed_at IS NOT NULL`, grant.ID); err != nil {
		t.Fatal(err)
	}
	if err = sqliteDB.Get(&audits, `SELECT COUNT(*) FROM exact_task_command_audits WHERE idempotency_key = ?`, grant.IdempotencyKey); err != nil {
		t.Fatal(err)
	}
	if consumed != 0 || audits != 0 {
		t.Fatalf("foreign evidence consumed=%d audits=%d", consumed, audits)
	}
}
