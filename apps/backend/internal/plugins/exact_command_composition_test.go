package plugins

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	goruntime "runtime"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/kandev/kandev/internal/db"
	"github.com/kandev/kandev/internal/plugins/pkgtar/pkgtartest"
	taskmodels "github.com/kandev/kandev/internal/task/models"
	tasksqlite "github.com/kandev/kandev/internal/task/repository/sqlite"
	taskservice "github.com/kandev/kandev/internal/task/service"
	"github.com/kandev/kandev/pkg/pluginsdk"
)

// TestRegisteredHostExactReceiptEnablesOnlyTheComposedSQLiteCommandPath pins
// the boot composition boundary: an installed plugin's Host read must persist
// the H6 receipt in the same SQLite authority that accepts the future command.
// UpdateTaskExact remains deliberately absent from the Host and SDK.
func TestRegisteredHostExactReceiptEnablesOnlyTheComposedSQLiteCommandPath(t *testing.T) {
	ctx := context.Background()
	repo, database := newExactCommandCompositionRepository(t)
	if err := repo.CreateWorkspace(ctx, &taskmodels.Workspace{ID: "workspace-1", Name: "Workspace"}); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	if err := repo.CreateTask(ctx, &taskmodels.Task{ID: "task-1", WorkspaceID: "workspace-1", Title: "Task", Description: "before"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	svc, _, _ := newTestService(t)
	svc.SetDataSources(exactCommandTaskSource{Repository: repo}, nil, nil, nil, nil, nil, nil, nil)
	bridge, err := NewSQLiteExactTaskCommandApprovalBridge(repo)
	if err != nil {
		t.Fatalf("NewSQLiteExactTaskCommandApprovalBridge: %v", err)
	}
	svc.SetExactTaskCommandApprovalBridge(bridge)
	record, err := svc.Install(ctx, exactTaskAccessPackage(t, "kandev-plugin-exact-command"))
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if _, err := svc.GrantCapabilityApproval(record.InstallationID, "workspace-1", 1, ManifestCapabilityDigest(record.Manifest), []string{"host.v2.read:tasks", "host.v2.write:tasks"}, "human", "test grant", "grant-audit"); err != nil {
		t.Fatalf("GrantCapabilityApproval: %v", err)
	}

	host, ok := svc.hostForPlugin(record.ID).(*pluginHost)
	if !ok {
		t.Fatal("hostForPlugin did not return a pluginHost")
	}
	items, _, err := host.ListTasksExact(ctx, pluginsdk.ExactTaskQuery{WorkspaceID: "workspace-1", CapabilityRevision: 1})
	if err != nil {
		t.Fatalf("ListTasksExact: %v", err)
	}
	if len(items) != 1 || items[0].ID != "task-1" {
		t.Fatalf("ListTasksExact items = %#v", items)
	}
	task, err := repo.GetTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	fence := exactCommandWorkspaceFence(t, database, "workspace-1")
	var receiptAuditID string
	if err := database.Get(&receiptAuditID, `SELECT audit_id FROM exact_task_command_receipts WHERE installation_id = ? AND workspace_id = ? AND capability_id = ? AND approval_revision = ?`, record.InstallationID, "workspace-1", "host.v2.read:tasks", 1); err != nil {
		t.Fatalf("read Host receipt from exact authority: %v", err)
	}
	grant := tasksqlite.ExactTaskCommandGrant{ID: "grant-1", InstallationID: record.InstallationID, WorkspaceID: "workspace-1", TaskID: task.ID, CapabilityID: "host.v2.read:tasks", ReceiptAuditID: receiptAuditID, ApprovalRevision: 1, ActionDigest: "marker-v1", IdempotencyKey: "command-1", ExpiresAt: time.Now().UTC().Add(time.Minute)}
	if err := repo.IssueExactTaskCommandGrant(ctx, grant); err != nil {
		t.Fatalf("IssueExactTaskCommandGrant: %v", err)
	}
	receipt, err := repo.ApplyExactTaskDescriptionCommand(ctx, tasksqlite.ExactTaskDescriptionCommand{GrantID: grant.ID, InstallationID: grant.InstallationID, WorkspaceID: grant.WorkspaceID, TaskID: grant.TaskID, CapabilityID: grant.CapabilityID, ReceiptAuditID: grant.ReceiptAuditID, ApprovalRevision: grant.ApprovalRevision, ActionDigest: grant.ActionDigest, IdempotencyKey: grant.IdempotencyKey, Marker: "[exact-marker]", ExpectedResourceVersion: task.ResourceVersion, ExpectedFence: fence})
	if err != nil {
		t.Fatalf("ApplyExactTaskDescriptionCommand: %v", err)
	}
	if receipt.AuditID != grant.IdempotencyKey || receipt.ResourceVersion != task.ResourceVersion+1 {
		t.Fatalf("command receipt = %+v", receipt)
	}
	stored, err := repo.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTask after command: %v", err)
	}
	if stored.Description != "[exact-marker]" {
		t.Fatalf("description = %q, want marker", stored.Description)
	}

	if err := repo.CreateTask(ctx, &taskmodels.Task{ID: "task-2", WorkspaceID: "workspace-1", Title: "Revoked task", Description: "unchanged"}); err != nil {
		t.Fatalf("CreateTask revoked task: %v", err)
	}
	revokedTask, err := repo.GetTask(ctx, "task-2")
	if err != nil {
		t.Fatalf("GetTask revoked task: %v", err)
	}
	revokedGrant := grant
	revokedGrant.ID, revokedGrant.TaskID, revokedGrant.ActionDigest, revokedGrant.IdempotencyKey = "grant-2", revokedTask.ID, "marker-v2", "command-2"
	if err := repo.IssueExactTaskCommandGrant(ctx, revokedGrant); err != nil {
		t.Fatalf("IssueExactTaskCommandGrant revoked grant: %v", err)
	}
	if _, err := svc.RevokeCapabilityApproval(record.InstallationID, "workspace-1", 1, "human", "test revoke", "revoke-audit"); err != nil {
		t.Fatalf("RevokeCapabilityApproval: %v", err)
	}
	_, err = repo.ApplyExactTaskDescriptionCommand(ctx, tasksqlite.ExactTaskDescriptionCommand{GrantID: revokedGrant.ID, InstallationID: revokedGrant.InstallationID, WorkspaceID: revokedGrant.WorkspaceID, TaskID: revokedGrant.TaskID, CapabilityID: revokedGrant.CapabilityID, ReceiptAuditID: revokedGrant.ReceiptAuditID, ApprovalRevision: revokedGrant.ApprovalRevision, ActionDigest: revokedGrant.ActionDigest, IdempotencyKey: revokedGrant.IdempotencyKey, Marker: "[revoked-marker]", ExpectedResourceVersion: revokedTask.ResourceVersion, ExpectedFence: exactCommandWorkspaceFence(t, database, "workspace-1")})
	if !errors.Is(err, tasksqlite.ErrExactTaskCommandUnavailable) {
		t.Fatalf("revoked command error = %v, want unavailable", err)
	}
	stored, err = repo.GetTask(ctx, revokedTask.ID)
	if err != nil {
		t.Fatalf("GetTask after revoked command: %v", err)
	}
	if stored.Description != "unchanged" {
		t.Fatalf("revoked command changed description to %q", stored.Description)
	}
}

func TestSQLiteExactTaskCommandBridgeRejectsNilAndPostgresAuthorities(t *testing.T) {
	if _, err := NewSQLiteExactTaskCommandApprovalBridge(nil); err == nil {
		t.Fatal("nil exact command authority was accepted")
	}
	raw, err := db.OpenSQLite(filepath.Join(t.TempDir(), "postgres-shaped.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	postgresShaped := sqlx.NewDb(raw, "pgx")
	t.Cleanup(func() { _ = postgresShaped.Close() })
	if _, err := NewSQLiteExactTaskCommandApprovalBridge(tasksqlite.NewReadOnlyWithDB(postgresShaped, nil)); err == nil {
		t.Fatal("PostgreSQL exact command authority was accepted")
	}
}

// exactCommandTaskSource fills only the Host's broader legacy data-source
// interface; this lifecycle test exercises its durable exact snapshot methods.
type exactCommandTaskSource struct{ *tasksqlite.Repository }

func (exactCommandTaskSource) BuildDependencyViews(context.Context, []*taskmodels.Task) map[string]taskservice.DependencyView {
	return nil
}

func (exactCommandTaskSource) BuildDependencyViewsBounded(context.Context, []*taskmodels.Task) (map[string]taskservice.DependencyView, error) {
	return nil, nil
}

func newExactCommandCompositionRepository(t *testing.T) (*tasksqlite.Repository, *sqlx.DB) {
	t.Helper()
	raw, err := db.OpenSQLite(filepath.Join(t.TempDir(), "exact-command.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	database := sqlx.NewDb(raw, "sqlite3")
	repo, err := tasksqlite.NewWithDB(database, database, nil)
	if err != nil {
		_ = database.Close()
		t.Fatalf("NewWithDB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return repo, database
}

func exactCommandWorkspaceFence(t *testing.T, database *sqlx.DB, workspaceID string) int64 {
	t.Helper()
	var fence int64
	if err := database.Get(&fence, `SELECT revision FROM exact_task_workspace_fences WHERE workspace_id = ?`, workspaceID); err != nil {
		t.Fatalf("read workspace fence: %v", err)
	}
	return fence
}

func exactTaskAccessPackage(t *testing.T, id string) *bytes.Buffer {
	t.Helper()
	platformKey := goruntime.GOOS + "-" + goruntime.GOARCH
	manifestYAML := fmt.Sprintf(`
id: %s
api_version: 1
version: 1.0.0
display_name: Exact command test plugin
min_kandev_version: "0.91.1"
capabilities:
  host_v2_read: ["tasks"]
  host_v2_write: ["tasks"]
runtime:
  type: binary
  executables:
    %s: server/plugin
`, id, platformKey)
	var packageBuffer bytes.Buffer
	if err := pkgtartest.WritePackage(&packageBuffer, map[string][]byte{"manifest.yaml": []byte(manifestYAML), "server/plugin": []byte("#!/bin/sh\necho fake\n")}); err != nil {
		t.Fatalf("WritePackage: %v", err)
	}
	return &packageBuffer
}
