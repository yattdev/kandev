package service

import (
	"context"
	"errors"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/kandev/kandev/internal/events"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository"
	sqliterepo "github.com/kandev/kandev/internal/task/repository/sqlite"
	"github.com/kandev/kandev/internal/worktree"
)

type workspaceOutcomeRepo struct {
	repository.WorkspaceRepository
	real      *sqliterepo.Repository
	committed bool
	failure   error
}

func (r *workspaceOutcomeRepo) DeleteWorkspaceCascadeWithName(ctx context.Context, id, name string) ([]*models.Task, []*models.Workflow, error) {
	if r.committed {
		if _, _, err := r.real.DeleteWorkspaceCascadeWithName(ctx, id, name); err != nil {
			return nil, nil, err
		}
		return nil, nil, r.failure
	}
	return r.real.DeleteWorkspaceCascadeWithNameAndSecretCleanup(ctx, id, name, func(ctx context.Context, tx *sqlx.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE tasks SET title = ? WHERE workspace_id = ?`, "rollback sentinel", id); err != nil {
			return err
		}
		return r.failure
	})
}

func TestWorkspaceCascadeCommitAndRollbackErrors(t *testing.T) {
	for _, committed := range []bool{true, false} {
		name := "rollback-error"
		if committed {
			name = "committed-error"
		}
		t.Run(name, func(t *testing.T) {
			svc, eventBus, repo := createTestService(t)
			svc.StopTaskResourceCleanupWorker()
			ctx := context.Background()
			const taskID = "workspace-outcome-task"
			const workspaceID = "ws-workspace-outcome-task"
			seedCleanupTaskAndSession(t, repo, taskID, "outcome-session")
			if err := repo.CreateWorkflow(ctx, &models.Workflow{ID: "outcome-workflow", WorkspaceID: workspaceID, Name: "Workflow"}); err != nil {
				t.Fatal(err)
			}
			workflowsBefore, err := repo.ListWorkflows(ctx, workspaceID, true)
			if err != nil {
				t.Fatal(err)
			}
			cleanup := &recordingWorktreeCleanup{worktreesByTaskID: map[string][]*worktree.Worktree{taskID: {{ID: "outcome-worktree", TaskID: taskID}}}}
			svc.SetWorktreeCleanup(cleanup)
			canvas := &retentionCanvasCleanup{}
			svc.SetCanvasCleanup(canvas)
			injected := errors.New("workspace cascade transport/rollback error")
			svc.workspaces = &workspaceOutcomeRepo{WorkspaceRepository: repo, real: repo, committed: committed, failure: injected}
			eventBus.ClearEvents()
			err = svc.DeleteWorkspaceWithConfirmName(ctx, workspaceID, "Workspace")
			if !errors.Is(err, injected) {
				t.Fatalf("service outcome=%v, want injected error", err)
			}
			workspace, workspaceErr := repo.GetWorkspace(ctx, workspaceID)
			task, taskErr := repo.GetTask(ctx, taskID)
			workspaceExists := workspaceErr == nil && workspace != nil
			taskExists := taskErr == nil && task != nil
			states := map[string]string{}
			rows, readErr := repo.DB().QueryContext(ctx, `SELECT operation_id,state FROM task_resource_cleanup_jobs ORDER BY operation_id`)
			if readErr != nil {
				t.Fatal(readErr)
			}
			for rows.Next() {
				var op, state string
				if err := rows.Scan(&op, &state); err != nil {
					_ = rows.Close()
					t.Fatal(err)
				}
				states[op] = state
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			// Explicitly run restart-style reconciliation after the failed response.
			resumeErr := svc.ResumeTaskResourceCleanupJobs(ctx)
			eventCounts := map[string]int{}
			for _, event := range eventBus.GetPublishedEvents() {
				eventCounts[event.Type]++
			}
			cleaned := cleanup.cleanedIDs()
			orphanErr := repo.CreateTask(ctx, &models.Task{ID: "after-workspace-outcome", WorkspaceID: workspaceID, Title: "Must not orphan"})
			var postCommit *CascadePostCommitError
			t.Logf("committed=%v classifiedPostCommit=%v workspaceExists=%v taskExists=%v jobs=%v resume=%v cleaned=%v canvasCalls=%d events=%v lateCreate=%v", committed, errors.As(err, &postCommit), workspaceExists, taskExists, states, resumeErr, cleaned, canvas.workspaceCalls, eventCounts, orphanErr)
			observation := workspaceOutcomeObservation{
				responseErr: err, workspaceExists: workspaceExists, taskExists: taskExists,
				task: task, states: states, orphanErr: orphanErr, cleaned: cleaned,
				eventCounts: eventCounts, workflowCount: len(workflowsBefore),
			}
			if committed {
				assertCommittedWorkspaceOutcome(t, observation)
			} else {
				assertRolledBackWorkspaceOutcome(t, observation)
			}
		})
	}
}

type workspaceOutcomeObservation struct {
	responseErr     error
	workspaceExists bool
	taskExists      bool
	task            *models.Task
	states          map[string]string
	orphanErr       error
	cleaned         []string
	eventCounts     map[string]int
	workflowCount   int
}

func assertCommittedWorkspaceOutcome(t *testing.T, o workspaceOutcomeObservation) {
	t.Helper()
	var postCommit *CascadePostCommitError
	if !errors.As(o.responseErr, &postCommit) {
		t.Error("committed cascade error was not classified as post-commit")
	}
	if o.workspaceExists || o.taskExists {
		t.Error("committed cascade rows survived")
	}
	for op, state := range o.states {
		if state == string(models.TaskResourceCleanupStateCancelled) {
			t.Errorf("committed cleanup cancelled: %s", op)
		}
	}
	if o.orphanErr == nil {
		t.Error("task creation succeeded after committed workspace deletion")
	}
	if len(o.cleaned) != 1 {
		t.Errorf("committed cleanup not recovered: cleaned=%v", o.cleaned)
	}
	if o.eventCounts[events.TaskDeleted] != 1 ||
		o.eventCounts[events.WorkflowDeleted] != o.workflowCount ||
		o.eventCounts[events.WorkspaceDeleted] != 1 {
		t.Errorf("committed deletion events missing: %v", o.eventCounts)
	}
}

func assertRolledBackWorkspaceOutcome(t *testing.T, o workspaceOutcomeObservation) {
	t.Helper()
	var postCommit *CascadePostCommitError
	if errors.As(o.responseErr, &postCommit) {
		t.Error("rolled-back cascade error was classified as post-commit")
	}
	if !o.workspaceExists || !o.taskExists || o.task.Title != "Task" {
		t.Errorf("rollback failed to preserve rows and title: workspace=%v task=%+v", o.workspaceExists, o.task)
	}
	for op, state := range o.states {
		if state != string(models.TaskResourceCleanupStateCancelled) {
			t.Errorf("uncommitted cleanup not cancelled: %s=%s", op, state)
		}
	}
	if len(o.cleaned) != 0 || o.eventCounts[events.TaskDeleted] != 0 ||
		o.eventCounts[events.WorkflowDeleted] != 0 || o.eventCounts[events.WorkspaceDeleted] != 0 {
		t.Errorf("rollback destructive effects: cleaned=%v events=%v", o.cleaned, o.eventCounts)
	}
	if o.orphanErr != nil {
		t.Errorf("cancelled rollback fence blocked live workspace creation: %v", o.orphanErr)
	}
}
