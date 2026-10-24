package sqlite

import (
	"context"
	"errors"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository/repoerrors"
)

func TestRetentionAdmission_ReservationRejectsHeldTaskAndConflictingReplay(t *testing.T) {
	repo := newBarrierTestRepo(t)
	ctx := context.Background()
	seedBarrierTask(t, repo, "retention-reserve")
	_, err := repo.db.ExecContext(ctx, repo.db.Rebind(`UPDATE tasks SET metadata = ? WHERE id = ?`),
		`{"terminal_retention":true}`, "retention-reserve")
	if err != nil {
		t.Fatal(err)
	}
	job := &models.TaskResourceCleanupJob{
		OperationID: "retention-op", TaskID: "retention-reserve",
		Trigger: models.TaskResourceCleanupTriggerArchive,
		State:   models.TaskResourceCleanupStatePrepared,
	}
	if err := repo.CreateTaskResourceCleanupJob(ctx, job); err == nil {
		t.Fatal("held task admitted a cleanup reservation")
	}
	var count int
	if err := repo.db.GetContext(ctx, &count, `SELECT COUNT(*) FROM task_resource_cleanup_jobs WHERE task_id = ?`, "retention-reserve"); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("cleanup rows after denied reservation = %d", count)
	}
	_, err = repo.db.ExecContext(ctx, repo.db.Rebind(`UPDATE tasks SET metadata = ? WHERE id = ?`),
		`{"terminal_retention":false}`, "retention-reserve")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateTaskResourceCleanupJob(ctx, job); err != nil {
		t.Fatalf("reserve clean task: %v", err)
	}
	conflict := &models.TaskResourceCleanupJob{
		OperationID: job.OperationID, TaskID: "different-task",
		Trigger: models.TaskResourceCleanupTriggerDelete,
		State:   models.TaskResourceCleanupStatePrepared,
	}
	if err := repo.CreateTaskResourceCleanupJob(ctx, conflict); err == nil {
		t.Fatal("conflicting operation replay accepted")
	}
}

func TestRetentionAdmission_WorkspaceOnlyCleanupKeepsDurableTransitions(t *testing.T) {
	repo := newBarrierTestRepo(t)
	ctx := context.Background()
	seedWorkspace(t, repo, "ws-1")
	job := &models.TaskResourceCleanupJob{
		OperationID: "workspace-attachments:one", Trigger: models.TaskResourceCleanupTriggerWorkspaceDelete,
		State: models.TaskResourceCleanupStatePrepared, ResourceSnapshot: `{"workspace_id":"ws-1"}`,
	}
	if err := repo.CreateTaskResourceCleanupJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if started, err := repo.StartPreparedTaskResourceCleanupJob(ctx, job.ID); err != nil || !started {
		t.Fatalf("start workspace cleanup: %v, %v", started, err)
	}
	if claimed, err := repo.MarkTaskResourceCleanupJobRunning(ctx, job.ID); err != nil || !claimed {
		t.Fatalf("claim workspace cleanup: %v, %v", claimed, err)
	}
	if err := repo.ResetRunningTaskResourceCleanupJobs(ctx); err != nil {
		t.Fatalf("reset workspace cleanup: %v", err)
	}
	if claimed, err := repo.MarkTaskResourceCleanupJobRunning(ctx, job.ID); err != nil || !claimed {
		t.Fatalf("reclaim workspace cleanup: %v, %v", claimed, err)
	}
	if completed, err := repo.CompleteClaimedTaskResourceCleanupJob(ctx, job.ID, 2,
		models.TaskResourceCleanupStateSucceeded, "", nil); err != nil || !completed {
		t.Fatalf("complete workspace cleanup: %v, %v", completed, err)
	}
}

func TestRetentionAdmission_HeldTaskBlocksPreparedStartClaimRestoreAndReset(t *testing.T) {
	repo := newBarrierTestRepo(t)
	ctx := context.Background()
	seedBarrierTask(t, repo, "retention-worker")
	states := []models.TaskResourceCleanupState{
		models.TaskResourceCleanupStatePrepared,
		models.TaskResourceCleanupStatePending,
		models.TaskResourceCleanupStateCancelled,
		models.TaskResourceCleanupStateRunning,
	}
	for _, state := range states {
		job := &models.TaskResourceCleanupJob{
			OperationID: "worker-" + string(state), TaskID: "retention-worker",
			Trigger: models.TaskResourceCleanupTriggerArchive, State: state,
		}
		if err := repo.CreateTaskResourceCleanupJob(ctx, job); err != nil {
			t.Fatal(err)
		}
	}
	_, err := repo.db.ExecContext(ctx, repo.db.Rebind(`UPDATE tasks SET metadata = ? WHERE id = ?`),
		`{"terminal_retention":true}`, "retention-worker")
	if err != nil {
		t.Fatal(err)
	}
	prepared, _ := repo.GetTaskResourceCleanupJobByOperationID(ctx, "worker-prepared")
	pending, _ := repo.GetTaskResourceCleanupJobByOperationID(ctx, "worker-pending")
	cancelled, _ := repo.GetTaskResourceCleanupJobByOperationID(ctx, "worker-cancelled")
	if _, err := repo.StartPreparedTaskResourceCleanupJob(ctx, prepared.ID); !errors.Is(err, errTaskRetentionHeld) {
		t.Fatalf("start held job: %v", err)
	}
	if _, err := repo.MarkTaskResourceCleanupJobRunning(ctx, pending.ID); !errors.Is(err, errTaskRetentionHeld) {
		t.Fatalf("claim held job: %v", err)
	}
	if _, err := repo.RestoreCancelledTaskResourceCleanupJobIfUnchanged(ctx, cancelled.ID, 0, ""); !errors.Is(err, errTaskRetentionHeld) {
		t.Fatalf("restore held job: %v", err)
	}
	if err := repo.ResetRunningTaskResourceCleanupJobs(ctx); !errors.Is(err, errTaskRetentionHeld) {
		t.Fatalf("reset held job: %v", err)
	}
	for _, state := range states {
		job, err := repo.GetTaskResourceCleanupJobByOperationID(ctx, "worker-"+string(state))
		if err != nil || job.State != state {
			t.Fatalf("job %s changed after denied transition: %+v, %v", state, job, err)
		}
	}
}

func TestRetentionAdmission_GenericMetadataCannotMutateHold(t *testing.T) {
	repo := newBarrierTestRepo(t)
	ctx := context.Background()
	seedBarrierTask(t, repo, "retention-key")
	if err := repo.SetTaskMetadataKey(ctx, "retention-key", models.MetaKeyTerminalRetention, true); !errors.Is(err, errReservedRetentionMetadata) {
		t.Fatalf("set reserved key: %v", err)
	}
	if _, err := repo.RemoveTaskMetadataKey(ctx, "retention-key", models.MetaKeyTerminalRetention); !errors.Is(err, errReservedRetentionMetadata) {
		t.Fatalf("remove reserved key: %v", err)
	}
}

func TestRetentionAdmission_MalformedHoldDeniesCleanupAndTaskUpdate(t *testing.T) {
	repo := newBarrierTestRepo(t)
	ctx := context.Background()
	seedBarrierTask(t, repo, "retention-malformed")
	_, err := repo.db.ExecContext(ctx, repo.db.Rebind(`UPDATE tasks SET metadata = ? WHERE id = ?`),
		`{"terminal_retention":null}`, "retention-malformed")
	if err != nil {
		t.Fatal(err)
	}
	job := &models.TaskResourceCleanupJob{
		OperationID: "malformed-op", TaskID: "retention-malformed",
		Trigger: models.TaskResourceCleanupTriggerArchive,
	}
	if err := repo.CreateTaskResourceCleanupJob(ctx, job); err == nil {
		t.Fatal("malformed hold admitted cleanup")
	}
	task, err := repo.GetTask(ctx, "retention-malformed")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateTask(ctx, task); err == nil {
		t.Fatal("malformed hold admitted whole-task write")
	}
}

func TestRetentionAdmission_HeldTaskPreventsArchiveAndDelete(t *testing.T) {
	repo := newBarrierTestRepo(t)
	ctx := context.Background()
	seedBarrierTask(t, repo, "retention-destructive")
	_, err := repo.db.ExecContext(ctx, repo.db.Rebind(`UPDATE tasks SET metadata = ? WHERE id = ?`),
		`{"terminal_retention":true}`, "retention-destructive")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.ArchiveTask(ctx, "retention-destructive"); !errors.Is(err, errTaskRetentionHeld) {
		t.Fatalf("archive held task: %v", err)
	}
	if err := repo.DeleteTask(ctx, "retention-destructive"); !errors.Is(err, errTaskRetentionHeld) {
		t.Fatalf("delete held task: %v", err)
	}
	task, err := repo.GetTask(ctx, "retention-destructive")
	if err != nil || task.ArchivedAt != nil {
		t.Fatalf("held task changed after denied mutations: %+v, %v", task, err)
	}
}

func TestRetentionAdmission_WorkspaceCascadeRejectsHeldTaskAtomically(t *testing.T) {
	repo := newRepoForHealTests(t)
	ctx := context.Background()
	seedWorkspaceCascadeRows(t, repo, "ws-delete")
	_, err := repo.db.ExecContext(ctx, repo.db.Rebind(`UPDATE tasks SET metadata = ? WHERE id = ?`),
		`{"terminal_retention":true}`, "task-delete")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.DeleteWorkspaceCascade(ctx, "ws-delete"); !errors.Is(err, errTaskRetentionHeld) {
		t.Fatalf("workspace cascade with held task: %v", err)
	}
	if _, err := repo.GetWorkspace(ctx, "ws-delete"); err != nil {
		t.Fatalf("workspace removed after denied cascade: %v", err)
	}
	if _, err := repo.GetTask(ctx, "task-delete"); err != nil {
		t.Fatalf("task removed after denied cascade: %v", err)
	}
}

func TestRetentionAdmission_StaleTaskSnapshotsCannotChangeAuthorizedHold(t *testing.T) {
	repo := newBarrierTestRepo(t)
	ctx := context.Background()
	seedBarrierTask(t, repo, "parent-retention")
	seedBarrierTask(t, repo, "child-retention")
	if _, err := repo.db.ExecContext(ctx, repo.db.Rebind(`
		UPDATE tasks SET parent_id = ? WHERE id = ?
	`), "parent-retention", "child-retention"); err != nil {
		t.Fatal(err)
	}
	staleFalse, err := repo.GetTask(ctx, "child-retention")
	if err != nil {
		t.Fatal(err)
	}
	setTrue, err := repo.GetTask(ctx, "child-retention")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateTaskWithParentRetention(ctx, setTrue, "parent-retention", "ws-1", true, false); err != nil {
		t.Fatalf("authorized true: %v", err)
	}
	if err := repo.UpdateTask(ctx, staleFalse); err != nil {
		t.Fatalf("stale ordinary update: %v", err)
	}
	current, err := repo.GetTask(ctx, "child-retention")
	if err != nil || !models.IsTerminalRetentionHeld(current.Metadata) {
		t.Fatalf("stale false removed live hold: task=%+v, err=%v", current, err)
	}
	staleTrue := current
	clear, err := repo.GetTask(ctx, "child-retention")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateTaskWithParentRetention(ctx, clear, "parent-retention", "ws-1", false, false); err != nil {
		t.Fatalf("authorized false: %v", err)
	}
	if err := repo.UpdateTask(ctx, staleTrue); err != nil {
		t.Fatalf("stale ordinary update after clear: %v", err)
	}
	current, err = repo.GetTask(ctx, "child-retention")
	if err != nil || models.IsTerminalRetentionHeld(current.Metadata) {
		t.Fatalf("stale true restored cleared hold: task=%+v, err=%v", current, err)
	}
}

func TestRetentionAdmission_ParentAndWorkspaceAreCheckedAtWrite(t *testing.T) {
	repo := newBarrierTestRepo(t)
	ctx := context.Background()
	seedBarrierTask(t, repo, "retention-parent")
	seedBarrierTask(t, repo, "retention-target")
	if _, err := repo.db.ExecContext(ctx, repo.db.Rebind(`
		UPDATE tasks SET parent_id = ? WHERE id = ?
	`), "retention-parent", "retention-target"); err != nil {
		t.Fatal(err)
	}
	stale, err := repo.GetTask(ctx, "retention-target")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.ExecContext(ctx, repo.db.Rebind(`
		UPDATE tasks SET parent_id = ? WHERE id = ?
	`), "another-parent", "retention-target"); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateTaskWithParentRetention(ctx, stale, "retention-parent", "ws-1", true, false); err == nil {
		t.Fatal("stale direct parent admitted retention")
	}
	var parent string
	if err := repo.db.GetContext(ctx, &parent, repo.db.Rebind(`SELECT parent_id FROM tasks WHERE id = ?`), "retention-target"); err != nil {
		t.Fatal(err)
	}
	if parent != "another-parent" {
		t.Fatalf("denied update rewrote parent to %q", parent)
	}
	stale, err = repo.GetTask(ctx, "retention-target")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.ExecContext(ctx, repo.db.Rebind(`
		UPDATE tasks SET workspace_id = ? WHERE id = ?
	`), "another-workspace", "retention-target"); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateTaskWithParentRetention(ctx, stale, "another-parent", "ws-1", true, false); err == nil {
		t.Fatal("stale workspace admitted retention")
	}
}

func TestRetentionAdmission_PreparedCleanupWinsOverParentHold(t *testing.T) {
	repo := newBarrierTestRepo(t)
	ctx := context.Background()
	seedBarrierTask(t, repo, "retention-parent")
	seedBarrierTask(t, repo, "retention-prepared")
	if _, err := repo.db.ExecContext(ctx, repo.db.Rebind(`
		UPDATE tasks SET parent_id = ? WHERE id = ?
	`), "retention-parent", "retention-prepared"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := repo.GetTask(ctx, "retention-prepared")
	if err != nil {
		t.Fatal(err)
	}
	reserveBarrier(t, repo, "retention-prepared", "retention-prepared-op")
	if err := repo.UpdateTaskWithParentRetention(ctx, snapshot, "retention-parent", "ws-1", true, false); err == nil {
		t.Fatal("hold overtook prepared cleanup")
	}
	current, err := repo.GetTask(ctx, "retention-prepared")
	if err != nil || models.IsTerminalRetentionHeld(current.Metadata) {
		t.Fatalf("denied hold changed task: %+v, %v", current, err)
	}
}

func TestRetentionAdmission_MissingTaskRequiresExactHistoricalJob(t *testing.T) {
	repo := newBarrierTestRepo(t)
	ctx := context.Background()
	job := &models.TaskResourceCleanupJob{
		OperationID: "missing-task-op", TaskID: "missing-task",
		Trigger: models.TaskResourceCleanupTriggerDelete, State: models.TaskResourceCleanupStatePrepared,
	}
	if err := repo.CreateTaskResourceCleanupJob(ctx, job); !errors.Is(err, repoerrors.ErrTaskNotFound) {
		t.Fatalf("fresh missing-task cleanup: %v", err)
	}
	seedBarrierTask(t, repo, "missing-task")
	if err := repo.CreateTaskResourceCleanupJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteTask(ctx, "missing-task"); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateTaskResourceCleanupJob(ctx, &models.TaskResourceCleanupJob{
		OperationID: job.OperationID, TaskID: job.TaskID, Trigger: job.Trigger, State: job.State,
	}); err != nil {
		t.Fatalf("exact historical replay: %v", err)
	}
	if err := repo.CreateTaskResourceCleanupJob(ctx, &models.TaskResourceCleanupJob{
		OperationID: job.OperationID, TaskID: job.TaskID, Trigger: models.TaskResourceCleanupTriggerArchive,
	}); !errors.Is(err, errCleanupReservationConflict) {
		t.Fatalf("conflicting historical replay: %v", err)
	}
	started, err := repo.StartPreparedTaskResourceCleanupJob(ctx, job.ID)
	if err != nil || !started {
		t.Fatalf("start exact preadmitted job after task deletion: %v, %v", started, err)
	}
	claimed, err := repo.MarkTaskResourceCleanupJobRunning(ctx, job.ID)
	if err != nil || !claimed {
		t.Fatalf("claim exact preadmitted job after task deletion: %v, %v", claimed, err)
	}
}

func TestRetentionAdmission_GenericCompletionCannotOverwriteClaim(t *testing.T) {
	repo := newBarrierTestRepo(t)
	ctx := context.Background()
	seedBarrierTask(t, repo, "retention-claim")
	job := &models.TaskResourceCleanupJob{
		OperationID: "retention-claim-op", TaskID: "retention-claim",
		Trigger: models.TaskResourceCleanupTriggerArchive, State: models.TaskResourceCleanupStatePending,
	}
	if err := repo.CreateTaskResourceCleanupJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if claimed, err := repo.MarkTaskResourceCleanupJobRunning(ctx, job.ID); err != nil || !claimed {
		t.Fatalf("claim: %v, %v", claimed, err)
	}
	if err := repo.CompleteTaskResourceCleanupJob(ctx, job.ID, models.TaskResourceCleanupStateCancelled, "", nil); !errors.Is(err, errCleanupReservationConflict) {
		t.Fatalf("generic completion overwrote claim: %v", err)
	}
	current, err := repo.GetTaskResourceCleanupJob(ctx, job.ID)
	if err != nil || current.State != models.TaskResourceCleanupStateRunning || current.Attempts != 1 {
		t.Fatalf("claim changed by generic completion: %+v, %v", current, err)
	}
}
