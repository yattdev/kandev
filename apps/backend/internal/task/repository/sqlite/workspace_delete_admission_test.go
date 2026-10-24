package sqlite

import (
	"context"
	"errors"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository/repoerrors"
)

func exerciseWorkspaceDeleteCreationFence(t *testing.T, first, second *Repository) {
	t.Helper()
	ctx := context.Background()
	const workspaceID = "workspace-delete-fence"
	seedWorkspace(t, first, workspaceID)
	job := &models.TaskResourceCleanupJob{
		OperationID:      models.WorkspaceDeleteAdmissionOperationID(workspaceID),
		Trigger:          models.TaskResourceCleanupTriggerWorkspaceDelete,
		State:            models.TaskResourceCleanupStatePrepared,
		ResourceSnapshot: `{"workspace_id":"workspace-delete-fence"}`,
	}
	if err := first.CreateTaskResourceCleanupJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	newTask := &models.Task{ID: "task-after-workspace-fence", WorkspaceID: workspaceID,
		Title: "Rejected", Priority: models.TaskPriorityMedium}
	if err := second.CreateTask(ctx, newTask); !errors.Is(err, repoerrors.ErrTaskCleanupInProgress) {
		t.Fatalf("post-fence task creation error = %v, want cleanup in progress", err)
	}
	if _, err := first.GetTask(ctx, newTask.ID); !errors.Is(err, repoerrors.ErrTaskNotFound) {
		t.Fatalf("post-fence task exists: %v", err)
	}
	cancelled, err := first.CancelTaskResourceCleanupJobIfPending(ctx, job.ID)
	if err != nil || !cancelled {
		t.Fatalf("cancel workspace fence = %v, %v", cancelled, err)
	}
	if err := second.CreateTask(ctx, newTask); err != nil {
		t.Fatalf("task creation after cancelled fence: %v", err)
	}
}

func TestWorkspaceDeleteCreationFence_TwoSQLiteConnections(t *testing.T) {
	first, second := newRetentionSQLitePair(t)
	exerciseWorkspaceDeleteCreationFence(t, first, second)
}

func TestWorkspaceDeleteCreationFence_TwoPostgresConnections(t *testing.T) {
	first, second, _ := newTaskPostgresRepoPair(t)
	exerciseWorkspaceDeleteCreationFence(t, first, second)
}
