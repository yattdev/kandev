package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"

	office "github.com/kandev/kandev/internal/office/models"
	officesqlite "github.com/kandev/kandev/internal/office/repository/sqlite"
	"github.com/kandev/kandev/internal/task/models"
	tasksqlite "github.com/kandev/kandev/internal/task/repository/sqlite"
)

func pendingCleanupExecutor(t *testing.T, tasks *tasksqlite.Repository) {
	t.Helper()
	if err := tasks.UpsertExecutorRunning(context.Background(), &models.ExecutorRunning{
		ID: "pending-cleanup-executor", SessionID: "pending-cleanup-session",
		TaskID: "pending-cleanup-task", Status: models.ExecutorRunningStatusRunning,
	}); err != nil {
		t.Fatal(err)
	}
}

func deferPendingCleanup(t *testing.T, handoff *HandoffService, tasks *tasksqlite.Repository) {
	t.Helper()
	pendingCleanupExecutor(t, tasks)
	if err := handoff.evaluateWorkspaceGroupCleanup(context.Background(), "pending-cleanup-group"); err != nil {
		t.Fatal(err)
	}
}

// pendingCleanupFixture uses the real task and office SQLite repositories;
// only the destructive filesystem operation is replaced with a spy.
func pendingCleanupFixtureWithService(t *testing.T) (*Service, *HandoffService, *officesqlite.Repository, *tasksqlite.Repository) {
	t.Helper()
	taskService, tasks := setupOfficeTest(t)
	t.Cleanup(taskService.StopTaskResourceCleanupWorker)
	ctx := context.Background()
	groupRepo, err := officesqlite.NewWithDB(sqlx.NewDb(tasks.DB(), "sqlite3"), sqlx.NewDb(tasks.DB(), "sqlite3"), nil)
	if err != nil {
		t.Fatal(err)
	}
	ws, err := tasks.GetWorkspace(ctx, "ws-1")
	if err != nil || ws == nil {
		t.Fatalf("GetWorkspace: %v", err)
	}
	task := &models.Task{ID: "pending-cleanup-task", WorkspaceID: ws.ID, WorkflowID: ws.OfficeWorkflowID,
		WorkflowStepID: "step", Title: "Archived member", Priority: models.TaskPriorityMedium}
	if err := tasks.CreateTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	if err := tasks.CreateTaskSession(ctx, &models.TaskSession{ID: "pending-cleanup-session", TaskID: task.ID, State: models.TaskSessionStateCompleted}); err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.DB().ExecContext(ctx, "UPDATE tasks SET archived_at = ? WHERE id = ?", time.Now().UTC(), task.ID); err != nil {
		t.Fatal(err)
	}
	group := &office.WorkspaceGroup{ID: "pending-cleanup-group", WorkspaceID: ws.ID, OwnerTaskID: task.ID,
		MaterializedKind: office.WorkspaceGroupKindPlainFolder, MaterializedPath: "/synthetic/pending-cleanup",
		OwnedByKandev: true, CleanupPolicy: office.WorkspaceCleanupPolicyDeleteWhenLastMemberArchivedOrDel,
		CleanupStatus: office.WorkspaceCleanupStatusActive}
	if err := groupRepo.CreateWorkspaceGroup(ctx, group); err != nil {
		t.Fatal(err)
	}
	if err := groupRepo.AddWorkspaceGroupMember(ctx, group.ID, task.ID, office.WorkspaceMemberRoleOwner); err != nil {
		t.Fatal(err)
	}
	if err := groupRepo.ReleaseWorkspaceGroupMember(ctx, group.ID, task.ID, office.WorkspaceReleaseReasonArchived, ""); err != nil {
		t.Fatal(err)
	}
	handoff := NewHandoffService(tasks, tasks, nil, nil, groupRepo, nil)
	handoff.SetSessionReader(tasks)
	return taskService, handoff, groupRepo, tasks
}

func pendingCleanupFixture(t *testing.T) (*HandoffService, *officesqlite.Repository, *tasksqlite.Repository) {
	t.Helper()
	_, handoff, groups, tasks := pendingCleanupFixtureWithService(t)
	return handoff, groups, tasks
}

func TestWorkspaceGroupPendingCleanupRetriesAfterExecutorStops(t *testing.T) {
	ctx := context.Background()
	handoff, groups, tasks := pendingCleanupFixture(t)
	// This executor row represents a real, still-owned session, not a timer.
	cleaner := &fakeWorkspaceCleaner{}
	handoff.SetWorkspaceCleaner(cleaner)
	deferPendingCleanup(t, handoff, tasks)
	group, err := groups.GetWorkspaceGroup(ctx, "pending-cleanup-group")
	if err != nil || group.CleanupStatus != office.WorkspaceCleanupStatusPending || len(cleaner.plainFolders) != 0 {
		t.Fatalf("deferred cleanup: group=%+v effects=%v err=%v", group, cleaner.plainFolders, err)
	}
	if err := tasks.DeleteExecutorRunningBySessionID(ctx, "pending-cleanup-session"); err != nil {
		t.Fatal(err)
	}
	running, err := tasks.HasExecutorRunningRow(ctx, "pending-cleanup-session")
	if err != nil || running {
		t.Fatalf("executor row still active: running=%v err=%v", running, err)
	}
	if err := handoff.evaluateWorkspaceGroupCleanup(ctx, "pending-cleanup-group"); err != nil {
		t.Fatal(err)
	}
	group, err = groups.GetWorkspaceGroup(ctx, "pending-cleanup-group")
	if err != nil || group.CleanupStatus != office.WorkspaceCleanupStatusCleaned || len(cleaner.plainFolders) != 1 {
		t.Fatalf("stopped executor must allow one cleanup: group=%+v effects=%v err=%v", group, cleaner.plainFolders, err)
	}
}

func TestWorkspaceGroupPendingCleanupRestartRetriesStoppedExecutor(t *testing.T) {
	ctx := context.Background()
	handoff, groups, tasks := pendingCleanupFixture(t)
	deferPendingCleanup(t, handoff, tasks)
	if err := tasks.DeleteExecutorRunningBySessionID(ctx, "pending-cleanup-session"); err != nil {
		t.Fatal(err)
	}
	// A fresh HandoffService has no process-local cursor or cleanup state.
	restarted := NewHandoffService(tasks, tasks, nil, nil, groups, nil)
	restarted.SetSessionReader(tasks)
	cleaner := &fakeWorkspaceCleaner{}
	restarted.SetWorkspaceCleaner(cleaner)
	if err := restarted.ReconcileDeferredWorkspaceGroupCleanups(ctx); err != nil {
		t.Fatal(err)
	}
	group, err := groups.GetWorkspaceGroup(ctx, "pending-cleanup-group")
	if err != nil || group.CleanupStatus != office.WorkspaceCleanupStatusCleaned || len(cleaner.plainFolders) != 1 {
		t.Fatalf("restart retry: group=%+v calls=%v err=%v", group, cleaner.plainFolders, err)
	}
	if err := restarted.ReconcileDeferredWorkspaceGroupCleanups(ctx); err != nil || len(cleaner.plainFolders) != 1 {
		t.Fatalf("repeated retry must be inert: calls=%v err=%v", cleaner.plainFolders, err)
	}
}

func TestWorkspaceGroupPendingCleanupTaskServiceReconcilerWiring(t *testing.T) {
	ctx := context.Background()
	taskService, handoff, groups, tasks := pendingCleanupFixtureWithService(t)
	deferPendingCleanup(t, handoff, tasks)
	if err := tasks.DeleteExecutorRunningBySessionID(ctx, "pending-cleanup-session"); err != nil {
		t.Fatal(err)
	}
	cleaner := &fakeWorkspaceCleaner{}
	handoff.SetWorkspaceCleaner(cleaner)
	taskService.SetTaskLifecycleCoordinator(handoff)
	taskService.reconcileDeferredWorkspaceGroupCleanups(ctx)
	group, err := groups.GetWorkspaceGroup(ctx, "pending-cleanup-group")
	if err != nil || group.CleanupStatus != office.WorkspaceCleanupStatusCleaned || len(cleaner.plainFolders) != 1 {
		t.Fatalf("task reconciliation wiring: group=%+v calls=%v err=%v", group, cleaner.plainFolders, err)
	}
}

type unreadablePendingSessions struct {
	SessionWorktreeReader
}

func (unreadablePendingSessions) HasExecutorRunningRow(context.Context, string) (bool, error) {
	return false, errors.New("synthetic executor read failure")
}

func TestWorkspaceGroupPendingCleanupPreservesLiveAndUnreadableExecutor(t *testing.T) {
	ctx := context.Background()
	handoff, groups, tasks := pendingCleanupFixture(t)
	cleaner := &fakeWorkspaceCleaner{}
	handoff.SetWorkspaceCleaner(cleaner)
	deferPendingCleanup(t, handoff, tasks)
	if err := handoff.ReconcileDeferredWorkspaceGroupCleanups(ctx); err != nil {
		t.Fatal(err)
	}
	if err := tasks.DeleteExecutorRunningBySessionID(ctx, "pending-cleanup-session"); err != nil {
		t.Fatal(err)
	}
	handoff.SetSessionReader(unreadablePendingSessions{tasks})
	if err := handoff.ReconcileDeferredWorkspaceGroupCleanups(ctx); err == nil {
		t.Fatal("unreadable executor inventory must deny cleanup")
	}
	group, err := groups.GetWorkspaceGroup(ctx, "pending-cleanup-group")
	if err != nil || group.CleanupStatus != office.WorkspaceCleanupStatusPending ||
		group.CleanupError != office.WorkspaceCleanupDeferredExecutorV1(group.OwnershipGeneration) || len(cleaner.plainFolders) != 0 {
		t.Fatalf("unknown executor must retain deferral: group=%+v calls=%v err=%v", group, cleaner.plainFolders, err)
	}
}

func TestWorkspaceGroupPendingCleanupRejectsGenerationDriftAndLiveMember(t *testing.T) {
	for _, tc := range []string{"generation", "member"} {
		t.Run(tc, func(t *testing.T) {
			ctx := context.Background()
			handoff, groups, tasks := pendingCleanupFixture(t)
			cleaner := &fakeWorkspaceCleaner{}
			handoff.SetWorkspaceCleaner(cleaner)
			deferPendingCleanup(t, handoff, tasks)
			if err := tasks.DeleteExecutorRunningBySessionID(ctx, "pending-cleanup-session"); err != nil {
				t.Fatal(err)
			}
			if tc == "generation" {
				_, err := tasks.DB().ExecContext(ctx, `UPDATE task_workspace_groups
					SET ownership_generation = ownership_generation + 1,
					owner_task_id = 'successor' WHERE id = 'pending-cleanup-group'`)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				_, err := tasks.DB().ExecContext(ctx, `UPDATE tasks SET archived_at = NULL WHERE id = 'pending-cleanup-task'`)
				if err != nil {
					t.Fatal(err)
				}
				_, err = tasks.DB().ExecContext(ctx, `UPDATE task_workspace_group_members
					SET released_at = NULL, release_reason = '', released_by_cascade_id = ''
					WHERE workspace_group_id = 'pending-cleanup-group' AND task_id = 'pending-cleanup-task'`)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := handoff.ReconcileDeferredWorkspaceGroupCleanups(ctx); err != nil {
				t.Fatal(err)
			}
			group, err := groups.GetWorkspaceGroup(ctx, "pending-cleanup-group")
			if err != nil || group.CleanupStatus != office.WorkspaceCleanupStatusPending || len(cleaner.plainFolders) != 0 {
				t.Fatalf("stale generation/live member: group=%+v calls=%v err=%v", group, cleaner.plainFolders, err)
			}
		})
	}
}

type onceFailingPendingCleaner struct {
	*fakeWorkspaceCleaner
	calls int
}

func (c *onceFailingPendingCleaner) CleanupPlainFolder(ctx context.Context, path string) error {
	c.calls++
	if c.calls == 1 {
		return errors.New("synthetic cleaner failure")
	}
	return c.fakeWorkspaceCleaner.CleanupPlainFolder(ctx, path)
}

func TestWorkspaceGroupPendingCleanupCleanerFailureRetries(t *testing.T) {
	ctx := context.Background()
	handoff, groups, tasks := pendingCleanupFixture(t)
	deferPendingCleanup(t, handoff, tasks)
	if err := tasks.DeleteExecutorRunningBySessionID(ctx, "pending-cleanup-session"); err != nil {
		t.Fatal(err)
	}
	cleaner := &onceFailingPendingCleaner{fakeWorkspaceCleaner: &fakeWorkspaceCleaner{}}
	handoff.SetWorkspaceCleaner(cleaner)
	if err := handoff.evaluateWorkspaceGroupCleanup(ctx, "pending-cleanup-group"); err == nil {
		t.Fatal("first cleaner call should fail")
	}
	group, err := groups.GetWorkspaceGroup(ctx, "pending-cleanup-group")
	if err != nil || group.CleanupStatus != office.WorkspaceCleanupStatusFailed || cleaner.calls != 1 {
		t.Fatalf("failed cleanup state: group=%+v calls=%d err=%v", group, cleaner.calls, err)
	}
	if err := handoff.evaluateWorkspaceGroupCleanup(ctx, "pending-cleanup-group"); err != nil {
		t.Fatal(err)
	}
	group, err = groups.GetWorkspaceGroup(ctx, "pending-cleanup-group")
	if err != nil || group.CleanupStatus != office.WorkspaceCleanupStatusCleaned || cleaner.calls != 2 || len(cleaner.plainFolders) != 1 {
		t.Fatalf("retry cleanup state: group=%+v calls=%d success=%v err=%v", group, cleaner.calls, cleaner.plainFolders, err)
	}
}

func TestWorkspaceGroupPendingCleanupRejectsLateExecutorAdmission(t *testing.T) {
	ctx := context.Background()
	handoff, groups, tasks := pendingCleanupFixture(t)
	deferPendingCleanup(t, handoff, tasks)
	if err := tasks.DeleteExecutorRunningBySessionID(ctx, "pending-cleanup-session"); err != nil {
		t.Fatal(err)
	}
	if err := tasks.UpsertExecutorRunning(ctx, &models.ExecutorRunning{
		ID: "late-executor", SessionID: "pending-cleanup-session", TaskID: "pending-cleanup-task",
		Status: models.ExecutorRunningStatusRunning,
	}); err == nil {
		t.Fatal("new executor must not reactivate during deferred cleanup")
	}
	group, err := groups.GetWorkspaceGroup(ctx, "pending-cleanup-group")
	if err != nil || group.CleanupStatus != office.WorkspaceCleanupStatusPending {
		t.Fatalf("late executor altered cleanup: group=%+v err=%v", group, err)
	}
}

func TestWorkspaceGroupPendingCleanupRejectsUnversionedReason(t *testing.T) {
	ctx := context.Background()
	handoff, groups, _ := pendingCleanupFixture(t)
	cleaner := &fakeWorkspaceCleaner{}
	handoff.SetWorkspaceCleaner(cleaner)
	if err := groups.UpdateWorkspaceGroupCleanupStatus(ctx, "pending-cleanup-group",
		office.WorkspaceCleanupStatusPending, "active executor still bound", nil); err != nil {
		t.Fatal(err)
	}
	if err := handoff.ReconcileDeferredWorkspaceGroupCleanups(ctx); err != nil {
		t.Fatal(err)
	}
	if err := handoff.evaluateWorkspaceGroupCleanup(ctx, "pending-cleanup-group"); err != nil {
		t.Fatal(err)
	}
	group, err := groups.GetWorkspaceGroup(ctx, "pending-cleanup-group")
	if err != nil || group.CleanupStatus != office.WorkspaceCleanupStatusPending ||
		group.CleanupError != "active executor still bound" || len(cleaner.plainFolders) != 0 {
		t.Fatalf("unversioned pending reason was claimed: group=%+v calls=%v err=%v", group, cleaner.plainFolders, err)
	}
}

func TestWorkspaceGroupPendingCleanupProtectsLiveCascadeMember(t *testing.T) {
	ctx := context.Background()
	handoff, groups, tasks := pendingCleanupFixture(t)
	cleaner := &fakeWorkspaceCleaner{}
	handoff.SetWorkspaceCleaner(cleaner)
	deferPendingCleanup(t, handoff, tasks)
	if err := tasks.DeleteExecutorRunningBySessionID(ctx, "pending-cleanup-session"); err != nil {
		t.Fatal(err)
	}
	// A committed partial cascade may leave a released membership with a
	// durable cascade ID while its task remains live. That row is a repair
	// obligation, not proof that cleanup may claim the group.
	if _, err := tasks.DB().ExecContext(ctx, `UPDATE task_workspace_group_members
		SET released_by_cascade_id = 'partial-cascade'
		WHERE workspace_group_id = 'pending-cleanup-group'`); err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.DB().ExecContext(ctx, `UPDATE tasks SET archived_at = NULL WHERE id = 'pending-cleanup-task'`); err != nil {
		t.Fatal(err)
	}
	if err := handoff.ReconcileDeferredWorkspaceGroupCleanups(ctx); err == nil {
		t.Fatal("live cascade member must retain a repair obligation")
	}
	group, err := groups.GetWorkspaceGroup(ctx, "pending-cleanup-group")
	if err != nil || group.CleanupStatus != office.WorkspaceCleanupStatusPending || len(cleaner.plainFolders) != 0 {
		t.Fatalf("live cascade member was cleaned: group=%+v calls=%v err=%v", group, cleaner.plainFolders, err)
	}
}

type heldPendingCleaner struct {
	*fakeWorkspaceCleaner
	entered chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func (c *heldPendingCleaner) CleanupPlainFolder(ctx context.Context, path string) error {
	c.calls.Add(1)
	select {
	case c.entered <- struct{}{}:
	default:
	}
	select {
	case <-c.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestWorkspaceGroupPendingCleanupDoesNotDuplicateInFlightClaim(t *testing.T) {
	ctx := context.Background()
	handoff, groups, tasks := pendingCleanupFixture(t)
	cleaner := &heldPendingCleaner{fakeWorkspaceCleaner: &fakeWorkspaceCleaner{}, entered: make(chan struct{}, 2), release: make(chan struct{})}
	defer func() {
		select {
		case <-cleaner.release:
		default:
			close(cleaner.release)
		}
	}()
	handoff.SetWorkspaceCleaner(cleaner)
	first := make(chan error, 1)
	go func() { first <- handoff.evaluateWorkspaceGroupCleanup(ctx, "pending-cleanup-group") }()
	select {
	case <-cleaner.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first cleanup never entered")
	}
	deferred, err := groups.DeferWorkspaceGroupCleanup(ctx, "pending-cleanup-group", 1)
	if err != nil || deferred {
		t.Fatalf("defer stole an in-flight claim: deferred=%v err=%v", deferred, err)
	}
	if err := tasks.UpsertExecutorRunning(ctx, &models.ExecutorRunning{
		ID: "late-claimed-executor", SessionID: "pending-cleanup-session",
		TaskID: "pending-cleanup-task", Status: models.ExecutorRunningStatusRunning,
	}); err == nil {
		t.Fatal("new executor admitted after cleanup claim")
	}
	secondCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := handoff.evaluateWorkspaceGroupCleanup(secondCtx, "pending-cleanup-group"); err != nil {
		t.Fatal(err)
	}
	group, err := groups.GetWorkspaceGroup(ctx, "pending-cleanup-group")
	if err != nil || group.CleanupStatus != office.WorkspaceCleanupStatusPending || cleaner.calls.Load() != 1 {
		t.Fatalf("in-flight claim duplicated: group=%+v calls=%d err=%v", group, cleaner.calls.Load(), err)
	}
	close(cleaner.release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	group, err = groups.GetWorkspaceGroup(ctx, "pending-cleanup-group")
	if err != nil || group.CleanupStatus != office.WorkspaceCleanupStatusCleaned || cleaner.calls.Load() != 1 {
		t.Fatalf("completed claim: group=%+v calls=%d err=%v", group, cleaner.calls.Load(), err)
	}
}
