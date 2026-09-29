package handlers

import (
	"context"
	"fmt"
	"testing"

	mcpscope "github.com/kandev/kandev/internal/mcp/scope"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/service"
	"github.com/stretchr/testify/require"
)

// @covers AC-FR-03
func TestVerifyLiveCallerReadsPersistedSessionState(t *testing.T) {
	ctx := context.Background()
	svc, repo := newTestTaskService(t)
	workspaces, err := svc.ListWorkspaces(ctx)
	require.NoError(t, err)
	require.Len(t, workspaces, 1)
	workflow, err := svc.CreateWorkflow(ctx, &service.CreateWorkflowRequest{
		WorkspaceID: workspaces[0].ID,
		Name:        "Live caller verification",
	})
	require.NoError(t, err)
	resolver := mcpscope.NewResolver(repo, nil, func() bool { return false }, testLogger(t))

	for _, state := range models.AllTaskSessionStates {
		t.Run(string(state), func(t *testing.T) {
			sessionID := "live-caller-session-" + string(state)
			created, err := svc.CreateTask(ctx, &service.CreateTaskRequest{
				WorkspaceID: workspaces[0].ID,
				WorkflowID:  workflow.ID,
				Title:       fmt.Sprintf("Live caller %s", state),
			})
			require.NoError(t, err)
			require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{
				ID: sessionID, TaskID: created.Task.ID, IsPrimary: true, State: state,
			}))

			principalCtx, err := resolver.ScopePrincipal(ctx, created.Task.ID, sessionID)
			require.NoError(t, err)
			_, err = resolver.VerifyLiveCaller(principalCtx)
			if state == models.TaskSessionStateStarting || state == models.TaskSessionStateRunning {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
		})
	}
}

// @covers AC-FR-03
func TestVerifyLiveCallerRejectsArchivedOwningTask(t *testing.T) {
	ctx := context.Background()
	svc, repo := newTestTaskService(t)
	workspaces, err := svc.ListWorkspaces(ctx)
	require.NoError(t, err)
	workflow, err := svc.CreateWorkflow(ctx, &service.CreateWorkflowRequest{
		WorkspaceID: workspaces[0].ID,
		Name:        "Archived live caller verification",
	})
	require.NoError(t, err)
	created, err := svc.CreateTask(ctx, &service.CreateTaskRequest{
		WorkspaceID: workspaces[0].ID,
		WorkflowID:  workflow.ID,
		Title:       "Archived live caller",
	})
	require.NoError(t, err)
	require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{
		ID: "archived-live-caller-session", TaskID: created.Task.ID, IsPrimary: true,
		State: models.TaskSessionStateRunning,
	}))
	resolver := mcpscope.NewResolver(repo, nil, func() bool { return false }, testLogger(t))
	principalCtx, err := resolver.ScopePrincipal(ctx, created.Task.ID, "archived-live-caller-session")
	require.NoError(t, err)

	require.NoError(t, repo.ArchiveTask(ctx, created.Task.ID))
	_, err = resolver.VerifyLiveCaller(principalCtx)
	require.Error(t, err)
}
