package sqlite

import (
	"context"
	"strings"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
)

func TestTaskCreationRejectsRetainedWorkspaceGroupIDAcrossAdmissions(t *testing.T) {
	repo := newRepoForEntityTests(t)
	ctx := context.Background()
	seedWorkspace(t, repo, "retained-ws")
	if err := repo.CreateWorkflow(ctx, &models.Workflow{ID: "retained-wf", WorkspaceID: "retained-ws", Name: "Workflow"}); err != nil {
		t.Fatal(err)
	}
	insertSelectionStep(t, repo, "retained-wf", "retained-step", 0)
	if _, err := repo.db.Exec(`CREATE TABLE task_workspace_group_members (task_id TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	create := []struct {
		name string
		call func(*models.Task) error
	}{
		{"ordinary", func(task *models.Task) error { return repo.CreateTask(ctx, task) }},
		{"capacity", func(task *models.Task) error {
			return repo.CreateTaskIfWorkflowStepHasCapacity(ctx, task, "retained-step", 10)
		}},
		{"placement", func(task *models.Task) error {
			return repo.CreateTaskWithWorkflowStepAdmission(ctx, task, "retained-step", 10, "", 0)
		}},
	}
	for _, tc := range create {
		t.Run(tc.name, func(t *testing.T) {
			id := "historical-" + tc.name
			initial := &models.Task{ID: id, WorkspaceID: "retained-ws", WorkflowID: "retained-wf", Title: "Initial"}
			if err := repo.CreateTask(ctx, initial); err != nil {
				t.Fatal(err)
			}
			if _, err := repo.db.Exec(`INSERT INTO task_workspace_group_members(task_id) VALUES (?)`, id); err != nil {
				t.Fatal(err)
			}
			if _, err := repo.db.Exec(`DELETE FROM tasks WHERE id = ?`, id); err != nil {
				t.Fatal(err)
			}
			reused := &models.Task{ID: id, WorkspaceID: "retained-ws", WorkflowID: "retained-wf", WorkflowStepID: "retained-step", Title: "Reused"}
			if err := tc.call(reused); err == nil || !strings.Contains(err.Error(), "retained in workspace group membership") {
				t.Fatalf("retained ID create error = %v, want membership denial", err)
			}
			var count int
			if err := repo.db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE id = ?`, id).Scan(&count); err != nil || count != 0 {
				t.Fatalf("retained task rows = %d, %v; want zero", count, err)
			}
			if err := repo.db.QueryRow(`SELECT COUNT(*) FROM task_step_transitions WHERE task_id = ?`, id).Scan(&count); err != nil || count != 0 {
				t.Fatalf("retained task transitions = %d, %v; want zero", count, err)
			}
			fresh := &models.Task{ID: "fresh-" + tc.name, WorkspaceID: "retained-ws", WorkflowID: "retained-wf", WorkflowStepID: "retained-step", Title: "Fresh"}
			if err := tc.call(fresh); err != nil {
				t.Fatalf("fresh ID create: %v", err)
			}
		})
	}
}

func TestTaskCreationAllowsTaskOnlyStoreAndDeniesMembershipReadError(t *testing.T) {
	ctx := context.Background()
	t.Run("task only", func(t *testing.T) {
		repo := newRepoForEntityTests(t)
		seedWorkspace(t, repo, "task-only-ws")
		available, err := workspaceGroupGuardTableExists(ctx, repo.db)
		if err != nil || available {
			t.Fatalf("task-only membership table = %v, %v; want absent", available, err)
		}
		if err := repo.CreateTask(ctx, &models.Task{ID: "task-only", WorkspaceID: "task-only-ws", Title: "Allowed"}); err != nil {
			t.Fatalf("task-only creation: %v", err)
		}
	})
	t.Run("read error", func(t *testing.T) {
		repo := newRepoForEntityTests(t)
		seedWorkspace(t, repo, "bad-view-ws")
		if _, err := repo.db.Exec(`CREATE VIEW task_workspace_group_members AS SELECT 1 AS unrelated`); err != nil {
			t.Fatal(err)
		}
		available, err := workspaceGroupGuardTableExists(ctx, repo.db)
		if err != nil || !available {
			t.Fatalf("malformed membership view probe = %v, %v; want available", available, err)
		}
		err = repo.CreateTask(ctx, &models.Task{ID: "unverified", WorkspaceID: "bad-view-ws", Title: "Denied"})
		if err == nil {
			t.Fatal("membership read error admitted task")
		}
		var count int
		if err := repo.db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE id = 'unverified'`).Scan(&count); err != nil || count != 0 {
			t.Fatalf("unverified task rows = %d, %v; want zero", count, err)
		}
	})
}
