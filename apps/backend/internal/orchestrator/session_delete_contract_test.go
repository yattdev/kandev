package orchestrator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/orchestrator/executor"
	"github.com/kandev/kandev/internal/task/models"
)

func TestDeleteSessionRejectsCurrentPrimary(t *testing.T) {
	ctx := context.Background()
	repo := setupTestRepo(t)
	seedTaskAndSession(t, repo, "task-primary-close", "session-primary", models.TaskSessionStateCompleted)
	if err := repo.CreateTaskSession(ctx, &models.TaskSession{
		ID: "session-helper", TaskID: "task-primary-close", State: models.TaskSessionStateCompleted,
	}); err != nil {
		t.Fatalf("CreateTaskSession helper: %v", err)
	}
	if err := repo.SetSessionPrimary(ctx, "session-primary"); err != nil {
		t.Fatalf("SetSessionPrimary: %v", err)
	}
	svc := createTestService(repo, newMockStepGetter(), newMockTaskRepo())

	err := svc.DeleteSession(ctx, "session-primary")
	if err == nil || !strings.Contains(err.Error(), "primary") {
		t.Fatalf("DeleteSession error = %v, want actionable primary-session rejection", err)
	}
	primary, err := repo.GetPrimarySessionByTaskID(ctx, "task-primary-close")
	if err != nil {
		t.Fatalf("GetPrimarySessionByTaskID: %v", err)
	}
	if primary.ID != "session-primary" {
		t.Fatalf("primary session changed after rejected close: %s", primary.ID)
	}
}

func TestDeleteSession_PreservesTaskWorkspaceAndNeverEnqueuesCleanup(t *testing.T) {
	ctx := context.Background()
	repo := setupTestRepo(t)
	seedTaskAndSession(t, repo, "task-session-only", "session-only", models.TaskSessionStateCompleted)
	now := time.Now().UTC()
	if err := repo.CreateRepository(ctx, &models.Repository{
		ID: "repo-session-only", WorkspaceID: "ws1", Name: "repo", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}
	if err := repo.CreateTaskEnvironment(ctx, &models.TaskEnvironment{
		ID: "env-session-only", TaskID: "task-session-only", ExecutorType: "worktree",
		WorkspacePath: "/tmp/ws-session-only", Status: models.TaskEnvironmentStatusReady,
		Repos: []*models.TaskEnvironmentRepo{{
			ID: "env-repo-session-only", RepositoryID: "repo-session-only",
			WorktreeID: "wt-session-only", WorktreePath: "/tmp/ws-session-only/repo",
			WorktreeBranch: "feature/x", Status: "active",
		}},
	}); err != nil {
		t.Fatalf("CreateTaskEnvironment: %v", err)
	}
	session, err := repo.GetTaskSession(ctx, "session-only")
	if err != nil {
		t.Fatalf("GetTaskSession: %v", err)
	}
	session.TaskEnvironmentID = "env-session-only"
	if err := repo.UpdateTaskSession(ctx, session); err != nil {
		t.Fatalf("link session: %v", err)
	}

	manager := &mockAgentManager{}
	svc := createTestServiceWithAgent(repo, newMockStepGetter(), newMockTaskRepo(), manager)
	svc.executor = executor.NewExecutor(manager, repo, testLogger(), executor.ExecutorConfig{})

	if err := svc.DeleteSession(ctx, "session-only"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	// The session row is gone.
	if _, err := repo.GetTaskSession(ctx, "session-only"); err == nil {
		t.Fatal("expected session to be gone after deletion")
	}

	// The task-owned environment and its repository worktree row survive.
	env, err := repo.GetTaskEnvironment(ctx, "env-session-only")
	if err != nil {
		t.Fatalf("GetTaskEnvironment: %v", err)
	}
	if len(env.Repos) != 1 || env.Repos[0].WorktreeID != "wt-session-only" || env.Repos[0].Status != "active" {
		t.Fatalf("task-owned worktree row lost: %+v", env.Repos)
	}

	// No cleanup job of any kind was reserved for the task.
	var jobCount int
	if err := repo.DB().QueryRow(
		`SELECT COUNT(*) FROM task_resource_cleanup_jobs WHERE task_id = 'task-session-only'`,
	).Scan(&jobCount); err != nil {
		t.Fatalf("count cleanup jobs: %v", err)
	}
	if jobCount != 0 {
		t.Fatalf("cleanup jobs = %d, want 0 — session deletion must not reserve cleanup", jobCount)
	}
}
