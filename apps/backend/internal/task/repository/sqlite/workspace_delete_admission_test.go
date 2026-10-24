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

func TestWorkspaceDeleteCreationFence_RejectsTaskAfterWorkspaceDeleted(t *testing.T) {
	for _, state := range []models.TaskResourceCleanupState{
		models.TaskResourceCleanupStateSucceeded, models.TaskResourceCleanupStateFailed,
	} {
		t.Run(string(state), func(t *testing.T) {
			repo := newBarrierTestRepo(t)
			ctx := context.Background()
			seedWorkspace(t, repo, "deleted-workspace")
			job := &models.TaskResourceCleanupJob{
				OperationID:      models.WorkspaceDeleteAdmissionOperationID("deleted-workspace"),
				Trigger:          models.TaskResourceCleanupTriggerWorkspaceDelete,
				State:            models.TaskResourceCleanupStatePrepared,
				ResourceSnapshot: `{"workspace_id":"deleted-workspace"}`,
			}
			if err := repo.CreateTaskResourceCleanupJob(ctx, job); err != nil {
				t.Fatal(err)
			}
			if err := repo.DeleteWorkspace(ctx, "deleted-workspace"); err != nil {
				t.Fatal(err)
			}
			if err := repo.CompleteTaskResourceCleanupJob(ctx, job.ID, state, "", nil); err != nil {
				t.Fatal(err)
			}
			task := &models.Task{ID: "orphan-after-delete", WorkspaceID: "deleted-workspace", Title: "Orphan"}
			if err := repo.CreateTask(ctx, task); !errors.Is(err, repoerrors.ErrTaskCleanupInProgress) {
				t.Fatalf("task creation after workspace delete = %v, want cleanup in progress", err)
			}
			var count int
			if err := repo.db.GetContext(ctx, &count, `SELECT COUNT(*) FROM tasks WHERE id = ?`, task.ID); err != nil || count != 0 {
				t.Fatalf("orphan task count = %d, %v", count, err)
			}
		})
	}
}

func TestWorkspaceDeleteCreationFence_CancelledReservationCanRetry(t *testing.T) {
	first, second := newRetentionSQLitePair(t)
	ctx := context.Background()
	const workspaceID = "workspace-delete-retry"
	seedWorkspace(t, first, workspaceID)
	newJob := func() *models.TaskResourceCleanupJob {
		return &models.TaskResourceCleanupJob{
			OperationID:      models.WorkspaceDeleteAdmissionOperationID(workspaceID),
			Trigger:          models.TaskResourceCleanupTriggerWorkspaceDelete,
			State:            models.TaskResourceCleanupStatePrepared,
			ResourceSnapshot: `{"workspace_id":"workspace-delete-retry"}`,
		}
	}
	initial := newJob()
	if err := first.CreateTaskResourceCleanupJob(ctx, initial); err != nil {
		t.Fatal(err)
	}
	if cancelled, err := first.CancelTaskResourceCleanupJobIfPending(ctx, initial.ID); err != nil || !cancelled {
		t.Fatalf("cancel first reservation = %t, %v", cancelled, err)
	}
	if err := second.CreateTaskResourceCleanupJob(ctx, newJob()); err != nil {
		t.Fatalf("retry cancelled reservation: %v", err)
	}
	job, err := first.GetTaskResourceCleanupJobByOperationID(ctx, initial.OperationID)
	if err != nil || job == nil || job.ID != initial.ID || job.State != models.TaskResourceCleanupStatePrepared {
		t.Fatalf("retried reservation = %+v, %v", job, err)
	}
	if err := first.CreateTaskResourceCleanupJob(ctx, newJob()); !errors.Is(err, repoerrors.ErrTaskCleanupInProgress) {
		t.Fatalf("second active reservation = %v, want in progress", err)
	}
	task := &models.Task{ID: "task-during-workspace-delete-retry", WorkspaceID: workspaceID, Title: "Blocked"}
	if err := second.CreateTask(ctx, task); !errors.Is(err, repoerrors.ErrTaskCleanupInProgress) {
		t.Fatalf("task create during retry = %v, want in progress", err)
	}
}

func TestWorkspaceDeleteCreationFence_RejectsSnapshotIdentityMismatch(t *testing.T) {
	repo := newBarrierTestRepo(t)
	seedWorkspace(t, repo, "workspace-a")
	seedWorkspace(t, repo, "workspace-b")
	job := &models.TaskResourceCleanupJob{
		OperationID:      models.WorkspaceDeleteAdmissionOperationID("workspace-a"),
		Trigger:          models.TaskResourceCleanupTriggerWorkspaceDelete,
		State:            models.TaskResourceCleanupStatePrepared,
		ResourceSnapshot: `{"workspace_id":"workspace-b"}`,
	}
	if err := repo.CreateTaskResourceCleanupJob(context.Background(), job); err == nil {
		t.Fatal("mismatched workspace fence identity accepted")
	}
	var count int
	if err := repo.db.GetContext(context.Background(), &count,
		`SELECT COUNT(*) FROM task_resource_cleanup_jobs WHERE operation_id = ?`, job.OperationID); err != nil || count != 0 {
		t.Fatalf("mismatched workspace fence count = %d, %v", count, err)
	}
}

func TestWorkspaceDeleteCreationFence_TwoPostgresConnections(t *testing.T) {
	first, second, _ := newTaskPostgresRepoPair(t)
	exerciseWorkspaceDeleteCreationFence(t, first, second)
}
