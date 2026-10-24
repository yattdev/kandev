package service

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository"
)

type missingCleanupReadback struct {
	repository.TaskResourceCleanupRepository
}

func (*missingCleanupReadback) GetTaskResourceCleanupJobByOperationID(context.Context, string) (*models.TaskResourceCleanupJob, error) {
	return nil, nil
}

func TestRetentionCleanupAdmission_ArchiveRequiresDurableStore(t *testing.T) {
	svc, repo := setupOfficeTest(t)
	ctx := context.Background()
	if err := repo.CreateWorkspace(ctx, &models.Workspace{ID: "retention-store-ws", Name: "Retention store"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateTask(ctx, &models.Task{ID: "retention-store-task", WorkspaceID: "retention-store-ws", Title: "Task", Priority: "medium"}); err != nil {
		t.Fatal(err)
	}
	svc.resourceCleanups = nil
	if err := svc.ArchiveTask(ctx, "retention-store-task"); err == nil {
		t.Fatal("archive succeeded without durable cleanup admission")
	}
	task, err := repo.GetTask(ctx, "retention-store-task")
	if err != nil || task.ArchivedAt != nil {
		t.Fatalf("archive changed task despite denied admission: %+v, %v", task, err)
	}
}

func TestRetentionCleanupAdmission_LegacyCallbackNeedsDurableInventory(t *testing.T) {
	svc, _ := setupOfficeTest(t)
	legacy := &recordingLegacyCleanup{}
	svc.worktreeCleanup = legacy
	if _, err := svc.gatherWorktreesForDelete(context.Background(), "held-or-unknown-task"); err == nil {
		t.Fatal("legacy callback path reported an inventory it cannot provide")
	}
	if legacy.calls != 0 {
		t.Fatalf("legacy cleanup ran before durable admission: %d calls", legacy.calls)
	}
}

func TestRetentionCleanupAdmission_MissingReservationReadbackHasNoDeleteEffects(t *testing.T) {
	svc, repo := setupOfficeTest(t)
	ctx := context.Background()
	if err := repo.CreateTask(ctx, &models.Task{ID: "missing-readback", WorkspaceID: "ws-1", Title: "Task"}); err != nil {
		t.Fatal(err)
	}
	canvas := &retentionCanvasCleanup{}
	svc.SetCanvasCleanup(canvas)
	svc.resourceCleanups = &missingCleanupReadback{TaskResourceCleanupRepository: repo}
	if err := svc.DeleteTask(ctx, "missing-readback"); err == nil {
		t.Fatal("delete succeeded without verified cleanup reservation")
	}
	if canvas.taskCalls != 0 {
		t.Fatalf("canvas cleanup ran before verified reservation: %d", canvas.taskCalls)
	}
	if _, err := repo.GetTask(ctx, "missing-readback"); err != nil {
		t.Fatalf("delete changed task without verified reservation: %v", err)
	}
}
