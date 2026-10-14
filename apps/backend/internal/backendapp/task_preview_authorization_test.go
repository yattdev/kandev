package backendapp

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/kandev/kandev/internal/common/config"
	taskmodels "github.com/kandev/kandev/internal/task/models"
	sqliterepo "github.com/kandev/kandev/internal/task/repository/sqlite"
	"github.com/kandev/kandev/internal/taskpreview"
	usermodels "github.com/kandev/kandev/internal/user/models"
	"github.com/stretchr/testify/require"
)

func newTaskPreviewAuthorizationFixture(t *testing.T) (routeParams, *sqliterepo.Repository, taskpreview.Grant, string) {
	t.Helper()
	ctx := context.Background()
	tasks, repo, _ := newRunSubscriptionCheckHarness(t)
	cfg := &config.Config{}
	cfg.Features.Auth = true
	authSvc := newEnabledAuthService(t, cfg)
	owner, err := authSvc.AdminCreateUser(ctx, "preview-owner@example.test", "synthetic-pass123", "Preview Owner", usermodels.RoleMember)
	require.NoError(t, err)
	other, err := authSvc.AdminCreateUser(ctx, "other-preview@example.test", "synthetic-pass123", "Other", usermodels.RoleMember)
	require.NoError(t, err)
	workspace := seedOwnedWorkspace(t, tasks, owner.ID)
	task := &taskmodels.Task{ID: "synthetic-preview-task", WorkspaceID: workspace, Title: "Preview binding"}
	require.NoError(t, repo.CreateTask(ctx, task))
	environment := &taskmodels.TaskEnvironment{TaskID: task.ID, ExecutorType: "worktree", WorkspacePath: filepath.Join(t.TempDir(), "owned"), Status: taskmodels.TaskEnvironmentStatusStopped}
	require.NoError(t, repo.CreateTaskEnvironment(ctx, environment))
	grant := taskpreview.Grant{TaskID: task.ID, WorkspaceID: workspace, UserID: owner.ID, EnvironmentID: environment.ID, TaskRoot: environment.WorkspacePath}
	return routeParams{taskSvc: tasks, authSvc: authSvc}, repo, grant, other.ID
}

// Contract coverage for the existing implementation using real auth and SQLite
// task ACLs. No executor/session, host Docker, network or application is started.
func TestTaskPreviewRealAuthorizationPersistsWhileParkedWithoutMutation(t *testing.T) {
	p, repo, grant, _ := newTaskPreviewAuthorizationFixture(t)
	ctx := context.Background()
	beforeTask, err := repo.GetTask(ctx, grant.TaskID)
	require.NoError(t, err)
	beforeEnvironment, err := repo.GetTaskEnvironmentByTaskID(ctx, grant.TaskID)
	require.NoError(t, err)
	require.NoError(t, authorizeTaskPreview(p, ctx, grant.UserID, grant))
	afterTask, err := repo.GetTask(ctx, grant.TaskID)
	require.NoError(t, err)
	afterEnvironment, err := repo.GetTaskEnvironmentByTaskID(ctx, grant.TaskID)
	require.NoError(t, err)
	require.Equal(t, beforeTask, afterTask)
	require.Equal(t, beforeEnvironment, afterEnvironment)
	require.Equal(t, taskmodels.TaskEnvironmentStatusStopped, afterEnvironment.Status)
}

func TestTaskPreviewRealAuthorizationRefusesForeignBindings(t *testing.T) {
	p, _, grant, other := newTaskPreviewAuthorizationFixture(t)
	for _, kind := range []string{"cross-user", "foreign-workspace-owner", "wrong-workspace", "wrong-environment", "wrong-root"} {
		t.Run(kind, func(t *testing.T) {
			g, user := grant, grant.UserID
			switch kind {
			case "cross-user":
				user = other
			case "foreign-workspace-owner":
				// Matching user/grant fields cannot bypass the real task ACL.
				g.UserID, user = other, other
			case "wrong-workspace":
				g.WorkspaceID = "different-workspace"
			case "wrong-environment":
				g.EnvironmentID = "replacement-environment"
			case "wrong-root":
				g.TaskRoot = filepath.Join(t.TempDir(), "another-task")
			}
			require.ErrorIs(t, authorizeTaskPreview(p, context.Background(), user, g), taskpreview.ErrDenied)
		})
	}
}

func TestTaskPreviewRealAuthorizationRechecksAccountRevocation(t *testing.T) {
	p, _, grant, _ := newTaskPreviewAuthorizationFixture(t)
	ctx := context.Background()
	require.NoError(t, authorizeTaskPreview(p, ctx, grant.UserID, grant))
	_, err := p.authSvc.AdminSetRoleStatus(ctx, grant.UserID, usermodels.RoleMember, usermodels.StatusDisabled)
	require.NoError(t, err)
	require.ErrorIs(t, authorizeTaskPreview(p, ctx, grant.UserID, grant), taskpreview.ErrDenied)
	p.authSvc = newDisabledAuthService(t)
	require.ErrorIs(t, authorizeTaskPreview(p, ctx, grant.UserID, grant), taskpreview.ErrDenied)
}
