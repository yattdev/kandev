package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository"
	"github.com/kandev/kandev/internal/task/repository/repoerrors"
)

type lateSessionInventory struct {
	repository.SessionRepository
	once      sync.Once
	after     func() error
	createErr error
}

func (r *lateSessionInventory) ListTaskSessions(ctx context.Context, id string) ([]*models.TaskSession, error) {
	sessions, err := r.SessionRepository.ListTaskSessions(ctx, id)
	if err == nil {
		r.once.Do(func() { r.createErr = r.after() })
	}
	return sessions, err
}

func TestDirectMutationReservesBeforeSessionInventory(t *testing.T) {
	for _, operation := range []string{"archive", "delete"} {
		t.Run(operation, func(t *testing.T) {
			svc, _, repo := createTestService(t)
			ctx := context.Background()
			seedCleanupTaskAndSession(t, repo, "inventory-task", "old-session")
			late := &models.TaskSession{ID: "late-session", TaskID: "inventory-task", State: models.TaskSessionStateRunning}
			sessions := &lateSessionInventory{SessionRepository: repo, after: func() error { return repo.CreateTaskSession(ctx, late) }}
			svc.sessions = sessions
			var err error
			if operation == "archive" {
				err = svc.ArchiveTask(ctx, late.TaskID)
			} else {
				err = svc.DeleteTask(ctx, late.TaskID)
			}
			if err != nil {
				t.Fatalf("direct %s failed: %v", operation, err)
			}
			if sessions.createErr != nil {
				if !errors.Is(sessions.createErr, repoerrors.ErrTaskCleanupInProgress) {
					t.Fatal(sessions.createErr)
				}
				return
			}
			var raw string
			if err := repo.DB().QueryRowContext(ctx, `SELECT resource_snapshot FROM task_resource_cleanup_jobs WHERE task_id = ?`, late.TaskID).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var snapshot taskResourceCleanupSnapshot
			if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
				t.Fatal(err)
			}
			for _, session := range snapshot.Sessions {
				if session.ID == late.ID {
					return
				}
			}
			current, currentErr := repo.GetTaskSession(ctx, late.ID)
			t.Fatalf("resource creation succeeded after session inventory but before cleanup admission; direct %s committed with snapshot missing %s; currentSession=%+v lookup=%v", operation, late.ID, current, currentErr)
		})
	}
}

type workspaceAttemptKey struct{}
type workspaceInventoryBarrier struct {
	repository.TaskRepository
	entered chan struct{}
	once    sync.Once
}

func (r *workspaceInventoryBarrier) ListTasksByWorkspace(ctx context.Context, workspaceID, workflowID, repositoryID, query string, page, pageSize int, sort string, includeArchived, includeEphemeral, onlyEphemeral, excludeConfig bool) ([]*models.Task, int, error) {
	if ctx.Value(workspaceAttemptKey{}) == "second" {
		return nil, 0, errors.New("second-attempt inventory failure")
	}
	tasks, total, err := r.TaskRepository.ListTasksByWorkspace(ctx, workspaceID, workflowID, repositoryID, query, page, pageSize, sort, includeArchived, includeEphemeral, onlyEphemeral, excludeConfig)
	if err == nil {
		r.once.Do(func() { close(r.entered) })
		<-ctx.Done()
		return nil, 0, ctx.Err()
	}
	return tasks, total, err
}
func TestConcurrentWorkspaceDeleteCannotCancelAnotherFence(t *testing.T) {
	svc, _, repo := createTestService(t)
	ctx := context.Background()
	const workspaceID = "shared-fence"
	if err := repo.CreateWorkspace(ctx, &models.Workspace{ID: workspaceID, Name: "Workspace"}); err != nil {
		t.Fatal(err)
	}
	tasks := &workspaceInventoryBarrier{TaskRepository: repo, entered: make(chan struct{})}
	svc.tasks = tasks
	firstCtx, cancelFirst := context.WithCancel(context.WithValue(ctx, workspaceAttemptKey{}, "first"))
	defer cancelFirst()
	first := make(chan error, 1)
	go func() { first <- svc.DeleteWorkspaceWithConfirmName(firstCtx, workspaceID, "Workspace") }()
	select {
	case <-tasks.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("first workspace inventory barrier timed out")
	}
	fence, err := repo.GetTaskResourceCleanupJobByOperationID(ctx, models.WorkspaceDeleteAdmissionOperationID(workspaceID))
	if err != nil {
		cancelFirst()
		<-first
		t.Fatal(err)
	}
	initialState := fence.State
	secondCtx := context.WithValue(ctx, workspaceAttemptKey{}, "second")
	secondErr := svc.DeleteWorkspaceWithConfirmName(secondCtx, workspaceID, "Workspace")
	after, readErr := repo.GetTaskResourceCleanupJobByOperationID(ctx, fence.OperationID)
	createErr := repo.CreateTask(ctx, &models.Task{ID: "task-after-second-cancel", WorkspaceID: workspaceID, Title: "Should remain fenced"})
	cancelFirst()
	firstErr := <-first
	if readErr != nil {
		t.Fatal(readErr)
	}
	t.Logf("initial=%s sharedID=%s afterSecond=%s second=%v first=%v lateCreate=%v", initialState, fence.ID, after.State, secondErr, firstErr, createErr)
	if !errors.Is(secondErr, repoerrors.ErrTaskCleanupInProgress) {
		t.Fatalf("unexpected second outcome: %v", secondErr)
	}
	if after.State != models.TaskResourceCleanupStatePrepared || !errors.Is(createErr, repoerrors.ErrTaskCleanupInProgress) {
		t.Fatalf("second delete cancelled first delete's still-owned fence: initial=%s afterSecond=%s lateCreate=%v", initialState, after.State, createErr)
	}
}
