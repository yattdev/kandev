package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/task/models"
)

type retentionCanvasCleanup struct{ taskCalls, workspaceCalls int }

func (c *retentionCanvasCleanup) CleanupTaskCanvases(context.Context, string) error {
	c.taskCalls++
	return nil
}

func (c *retentionCanvasCleanup) CleanupWorkspaceCanvases(context.Context, string) error {
	c.workspaceCalls++
	return nil
}

func TestDirectCleanupWithoutPreparedAdmissionHasNoEffects(t *testing.T) {
	svc, repo := setupOfficeTest(t)
	ctx := context.Background()
	if err := repo.CreateTask(ctx, &models.Task{ID: "unreserved-cleanup", WorkspaceID: "ws-1", Title: "Unreserved"}); err != nil {
		t.Fatal(err)
	}
	canvas := &retentionCanvasCleanup{}
	svc.SetCanvasCleanup(canvas)
	if err := svc.CleanupTaskResources(ctx, "unreserved-cleanup", true); !errors.Is(err, ErrTaskCleanupAdmissionRequired) {
		t.Fatalf("direct cleanup error = %v, want admission required", err)
	}
	if canvas.taskCalls != 0 {
		t.Fatalf("unreserved cleanup invoked canvas callback %d times", canvas.taskCalls)
	}
	if _, err := repo.GetTask(ctx, "unreserved-cleanup"); err != nil {
		t.Fatalf("unreserved cleanup changed task: %v", err)
	}
}

func TestDirectCleanupStartsExactPreparedAdmission(t *testing.T) {
	svc, repo := setupOfficeTest(t)
	ctx := context.Background()
	if err := repo.CreateTask(ctx, &models.Task{ID: "prepared-cleanup", WorkspaceID: "ws-1", Title: "Prepared"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.PrepareTaskResourceCleanup(ctx, "prepared-cleanup", models.TaskResourceCleanupTriggerCascadeArchive, "prepared-cleanup-op", false); err != nil {
		t.Fatal(err)
	}
	if err := svc.CleanupTaskResources(ctx, "prepared-cleanup", false); !errors.Is(err, ErrTaskCleanupAdmissionRequired) {
		t.Fatalf("pre-archive cleanup error = %v, want admission required", err)
	}
	if err := repo.ArchiveTask(ctx, "prepared-cleanup"); err != nil {
		t.Fatal(err)
	}
	if err := svc.CleanupTaskResources(ctx, "prepared-cleanup", false); err != nil {
		t.Fatalf("prepared direct cleanup: %v", err)
	}
	job, err := repo.GetTaskResourceCleanupJobByOperationID(ctx, "prepared-cleanup-op")
	if err != nil || job == nil || job.State == models.TaskResourceCleanupStatePrepared {
		t.Fatalf("prepared job was not started: %+v, %v", job, err)
	}
}

func TestTerminalRetentionWorkspaceDeleteLeavesCanvasAuthority(t *testing.T) {
	svc, repo := setupOfficeTest(t)
	ctx := context.Background()
	workspace, err := repo.GetWorkspace(ctx, "ws-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range []*models.Task{
		{ID: "retention-ws-parent", WorkspaceID: workspace.ID, Title: "Parent"},
		{ID: "retention-ws-child", WorkspaceID: workspace.ID, ParentID: "retention-ws-parent", Title: "Child"},
	} {
		if err := repo.CreateTask(ctx, task); err != nil {
			t.Fatal(err)
		}
	}
	held := true
	if _, err := svc.UpdateTask(ctx, "retention-ws-child", &UpdateTaskRequest{TerminalRetention: &held,
		RetentionParentTaskID: "retention-ws-parent", RetentionWorkspaceID: workspace.ID}); err != nil {
		t.Fatal(err)
	}
	canvas := &retentionCanvasCleanup{}
	svc.SetCanvasCleanup(canvas)
	if err := svc.deleteWorkspace(ctx, workspace, nil); err == nil {
		t.Fatal("workspace deletion with held task succeeded")
	}
	if canvas.workspaceCalls != 0 {
		t.Fatalf("held workspace delete revoked canvas authority %d times", canvas.workspaceCalls)
	}
	if _, err := repo.GetWorkspace(ctx, workspace.ID); err != nil {
		t.Fatalf("held workspace was deleted: %v", err)
	}
}

func TestTerminalRetentionPreventsArchiveCleanupUntilCleared(t *testing.T) {
	svc, repo := setupOfficeTest(t)
	ctx := context.Background()
	workspace, err := repo.GetWorkspace(ctx, "ws-1")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	const stepID = "step-terminal-retention"
	if _, err := repo.DB().ExecContext(ctx, `
		INSERT INTO workflow_steps (id, workflow_id, name, position, auto_archive_after_hours, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, stepID, workspace.OfficeWorkflowID, "Done", 0, 1, now, now); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"retention-parent", "held-task", "ordinary-task"} {
		parentID := ""
		if id == "held-task" {
			parentID = "retention-parent"
		}
		if err := repo.CreateTask(ctx, &models.Task{
			ID: id, WorkspaceID: workspace.ID, WorkflowID: workspace.OfficeWorkflowID,
			WorkflowStepID: stepID, Title: id, ParentID: parentID,
		}); err != nil {
			t.Fatal(err)
		}
	}
	held := true
	if _, err := svc.UpdateTask(ctx, "held-task", &UpdateTaskRequest{TerminalRetention: &held,
		RetentionParentTaskID: "retention-parent", RetentionWorkspaceID: workspace.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DB().ExecContext(ctx,
		`UPDATE tasks SET updated_at = ? WHERE id IN (?, ?)`, now.Add(-48*time.Hour), "held-task", "ordinary-task"); err != nil {
		t.Fatal(err)
	}
	handoff := NewHandoffService(repo, repo, nil, nil, nil, nil)
	handoff.SetTaskResourceCleaner(svc)
	svc.SetAutoArchiveCoordinator(handoff)
	svc.runAutoArchive(ctx)
	for id, wantArchived := range map[string]bool{"held-task": false, "ordinary-task": true} {
		task, err := repo.GetTask(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if (task.ArchivedAt != nil) != wantArchived {
			t.Fatalf("%s archived = %v, want %v", id, task.ArchivedAt != nil, wantArchived)
		}
	}
	if err := svc.ArchiveTask(ctx, "held-task"); !errors.Is(err, ErrTaskArchiveHeld) {
		t.Fatalf("direct archive error = %v, want retention hold", err)
	}
	canvas := &retentionCanvasCleanup{}
	svc.SetCanvasCleanup(canvas)
	if err := svc.DeleteTask(ctx, "held-task"); !errors.Is(err, ErrTaskArchiveHeld) {
		t.Fatalf("direct delete error = %v, want retention hold", err)
	}
	if canvas.taskCalls != 0 {
		t.Fatalf("held delete revoked canvas authority %d times", canvas.taskCalls)
	}
	if _, err := repo.GetTask(ctx, "held-task"); err != nil {
		t.Fatalf("held task was deleted: %v", err)
	}
	if _, err := handoff.ArchiveTaskTree(ctx, "held-task", false); !errors.Is(err, ErrTaskArchiveHeld) {
		t.Fatalf("cascade archive error = %v, want retention hold", err)
	}
	held = false
	if _, err := svc.UpdateTask(ctx, "held-task", &UpdateTaskRequest{TerminalRetention: &held,
		RetentionParentTaskID: "retention-parent", RetentionWorkspaceID: workspace.ID}); err != nil {
		t.Fatal(err)
	}
	if err := svc.ArchiveTask(ctx, "held-task"); err != nil {
		t.Fatalf("archive after clearing hold: %v", err)
	}
}
