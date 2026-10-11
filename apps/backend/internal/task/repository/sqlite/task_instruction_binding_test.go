package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/kandev/kandev/internal/db"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/taskinstructions"
)

// @covers AC-3 and AC-6
func TestPutTaskInstructionBindingExactFencesVersionAndEnvironmentGeneration(t *testing.T) {
	repo, _ := newReachabilityTestRepo(t)
	ctx := context.Background()
	seedInstructionBindingTask(t, repo)
	task := taskForInstructionBinding(t, repo)

	first, err := repo.PutTaskInstructionBindingExact(ctx, taskinstructions.WriteRequest{
		TaskID: "task-instructions", WorkspaceID: "workspace-instructions", EnvironmentID: "env-instructions",
		ExpectedTaskResourceVersion: task.UpdatedAt.UTC().Format(time.RFC3339Nano),
		ManagementInstallationID:    "installation-instructions", ManagementInstanceKey: "instance-instructions", ExpectedClaimGeneration: 1,
		ExpectedOwnershipGeneration: 1, ExpectedVersion: 0, Content: "first", Digest: taskinstructions.Digest("first"),
	})
	if err != nil {
		t.Fatalf("first PutTaskInstructionBindingExact() error = %v", err)
	}
	if first.Version != 1 {
		t.Fatalf("first version = %d, want 1", first.Version)
	}

	if _, err := repo.PutTaskInstructionBindingExact(ctx, taskinstructions.WriteRequest{
		TaskID: "task-instructions", WorkspaceID: "workspace-instructions", EnvironmentID: "env-instructions",
		ExpectedTaskResourceVersion: task.UpdatedAt.UTC().Format(time.RFC3339Nano),
		ManagementInstallationID:    "installation-instructions", ManagementInstanceKey: "instance-instructions", ExpectedClaimGeneration: 1,
		ExpectedOwnershipGeneration: 1, ExpectedVersion: 0, Content: "stale", Digest: taskinstructions.Digest("stale"),
	}); !errors.Is(err, taskinstructions.ErrVersionConflict) {
		t.Fatalf("stale write error = %v, want version conflict", err)
	}

	second, err := repo.PutTaskInstructionBindingExact(ctx, taskinstructions.WriteRequest{
		TaskID: "task-instructions", WorkspaceID: "workspace-instructions", EnvironmentID: "env-instructions",
		ExpectedTaskResourceVersion: task.UpdatedAt.UTC().Format(time.RFC3339Nano),
		ManagementInstallationID:    "installation-instructions", ManagementInstanceKey: "instance-instructions", ExpectedClaimGeneration: 1,
		ExpectedOwnershipGeneration: 1, ExpectedVersion: 1, Content: "second", Digest: taskinstructions.Digest("second"),
	})
	if err != nil {
		t.Fatalf("second PutTaskInstructionBindingExact() error = %v", err)
	}
	if second.Version != 2 || second.Content != "second" {
		t.Fatalf("second binding = %+v, want version 2 with new content", second)
	}

	if _, err := repo.db.ExecContext(ctx, `UPDATE task_environments SET ownership_generation = 2 WHERE id = 'env-instructions'`); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.PutTaskInstructionBindingExact(ctx, taskinstructions.WriteRequest{
		TaskID: "task-instructions", WorkspaceID: "workspace-instructions", EnvironmentID: "env-instructions",
		ExpectedTaskResourceVersion: task.UpdatedAt.UTC().Format(time.RFC3339Nano),
		ManagementInstallationID:    "installation-instructions", ManagementInstanceKey: "instance-instructions", ExpectedClaimGeneration: 1,
		ExpectedOwnershipGeneration: 1, ExpectedVersion: 2, Content: "foreign", Digest: taskinstructions.Digest("foreign"),
	}); !errors.Is(err, taskinstructions.ErrEnvironmentChanged) {
		t.Fatalf("stale environment write error = %v, want environment conflict", err)
	}

	got, err := repo.GetTaskInstructionBinding(ctx, "task-instructions")
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 2 || got.Content != "second" {
		t.Fatalf("binding changed after rejected write: %+v", got)
	}
}

// @covers AC-2 and AC-3
func TestPutTaskInstructionBindingExactRejectsStaleTaskResourceVersion(t *testing.T) {
	repo, _ := newReachabilityTestRepo(t)
	ctx := context.Background()
	seedInstructionBindingTask(t, repo)
	task := taskForInstructionBinding(t, repo)

	if _, err := repo.db.ExecContext(ctx, `UPDATE tasks SET updated_at = ? WHERE id = ?`, task.UpdatedAt.Add(time.Second), task.ID); err != nil {
		t.Fatal(err)
	}
	_, err := repo.PutTaskInstructionBindingExact(ctx, taskinstructions.WriteRequest{
		TaskID: task.ID, WorkspaceID: task.WorkspaceID, EnvironmentID: "env-instructions",
		ExpectedTaskResourceVersion: task.UpdatedAt.UTC().Format(time.RFC3339Nano),
		ManagementInstallationID:    "installation-instructions", ManagementInstanceKey: "instance-instructions", ExpectedClaimGeneration: 1,
		ExpectedOwnershipGeneration: 1, ExpectedVersion: 0, Content: "stale task", Digest: taskinstructions.Digest("stale task"),
	})
	if !errors.Is(err, taskinstructions.ErrTaskChanged) {
		t.Fatalf("stale task write error = %v, want task changed", err)
	}
}

// @covers AC-2
func TestPutTaskInstructionBindingExactRejectsChangedManagementClaim(t *testing.T) {
	repo, _ := newReachabilityTestRepo(t)
	ctx := context.Background()
	seedInstructionBindingTask(t, repo)
	task := taskForInstructionBinding(t, repo)
	claim, err := repo.GetTaskManagementClaim(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ChangeTaskManagementClaim(ctx, models.TaskManagementClaimChange{
		Action: models.TaskManagementClaimRelease, TaskID: task.ID, WorkspaceID: task.WorkspaceID,
		ExpectedTaskResourceVersion:  task.UpdatedAt.UTC().Format(time.RFC3339Nano),
		ExpectedClaimResourceVersion: claim.ResourceVersion, InstanceKey: claim.InstanceKey,
		ActorID: "plugin:installation-instructions", ActorInstallationID: "installation-instructions", ActorInstanceKey: claim.InstanceKey,
	}); err != nil {
		t.Fatal(err)
	}
	_, err = repo.PutTaskInstructionBindingExact(ctx, taskinstructions.WriteRequest{
		TaskID: task.ID, WorkspaceID: task.WorkspaceID, EnvironmentID: "env-instructions",
		ExpectedTaskResourceVersion: task.UpdatedAt.UTC().Format(time.RFC3339Nano),
		ManagementInstallationID:    "installation-instructions", ManagementInstanceKey: "instance-instructions", ExpectedClaimGeneration: 1,
		ExpectedOwnershipGeneration: 1, ExpectedVersion: 0, Content: "stale claim", Digest: taskinstructions.Digest("stale claim"),
	})
	if !errors.Is(err, taskinstructions.ErrManagementClaimChanged) {
		t.Fatalf("stale management claim write error = %v, want claim changed", err)
	}
}

// @covers AC-3
func TestTaskInstructionBindingReadbackSurvivesRepositoryReopen(t *testing.T) {
	repo, path := newReachabilityTestRepo(t)
	ctx := context.Background()
	seedInstructionBindingTask(t, repo)
	written, err := repo.PutTaskInstructionBindingExact(ctx, taskinstructions.WriteRequest{
		TaskID: "task-instructions", WorkspaceID: "workspace-instructions", EnvironmentID: "env-instructions",
		ExpectedTaskResourceVersion: taskForInstructionBinding(t, repo).UpdatedAt.UTC().Format(time.RFC3339Nano),
		ManagementInstallationID:    "installation-instructions", ManagementInstanceKey: "instance-instructions", ExpectedClaimGeneration: 1,
		ExpectedOwnershipGeneration: 1, ExpectedVersion: 0, Content: "survives restart", Digest: taskinstructions.Digest("survives restart"),
	})
	if err != nil {
		t.Fatal(err)
	}

	raw, err := db.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	reopenedDB := sqlx.NewDb(raw, "sqlite3")
	t.Cleanup(func() { _ = reopenedDB.Close() })
	reopened, err := NewWithDB(reopenedDB, reopenedDB, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reopened.GetTaskInstructionBinding(ctx, written.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != written.Version || got.Digest != written.Digest || got.Content != written.Content || got.EnvironmentID != written.EnvironmentID {
		t.Fatalf("reopened binding = %+v, want %+v", got, written)
	}
}

func taskForInstructionBinding(t *testing.T, repo *Repository) *models.Task {
	t.Helper()
	task, err := repo.GetTask(context.Background(), "task-instructions")
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func seedInstructionBindingTask(t *testing.T, repo *Repository) {
	t.Helper()
	ctx := context.Background()
	if err := repo.CreateTask(ctx, &models.Task{ID: "task-instructions", WorkspaceID: "workspace-instructions", Title: "Instructions"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateTaskEnvironment(ctx, &models.TaskEnvironment{
		ID: "env-instructions", TaskID: "task-instructions", OwnershipGeneration: 1,
		ExecutorType: string(models.ExecutorTypeLocal), Status: models.TaskEnvironmentStatusCreating,
	}); err != nil {
		t.Fatal(err)
	}
	task := taskForInstructionBinding(t, repo)
	if _, err := repo.ChangeTaskManagementClaim(ctx, models.TaskManagementClaimChange{
		Action: models.TaskManagementClaimAcquire, TaskID: task.ID, WorkspaceID: task.WorkspaceID,
		ExpectedTaskResourceVersion: task.UpdatedAt.UTC().Format(time.RFC3339Nano),
		InstallationID:              "installation-instructions", InstanceKey: "instance-instructions",
		ActorID: "plugin:installation-instructions", ActorInstallationID: "installation-instructions", ActorInstanceKey: "instance-instructions",
	}); err != nil {
		t.Fatal(err)
	}
}
