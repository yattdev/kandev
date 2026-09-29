package scope

import (
	"context"
	"testing"

	mcpprofile "github.com/kandev/kandev/internal/mcp/profile"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

type principalLookup struct {
	task      *models.Task
	workspace *models.Workspace
	session   *models.TaskSession
}

func (l principalLookup) GetTask(context.Context, string) (*models.Task, error) {
	return l.task, nil
}

func (l principalLookup) GetWorkspace(context.Context, string) (*models.Workspace, error) {
	return l.workspace, nil
}

func (l principalLookup) GetTaskSession(context.Context, string) (*models.TaskSession, error) {
	return l.session, nil
}

func TestScopePrincipalDerivesAutomationIdentityFromExecution(t *testing.T) {
	resolver := &Resolver{tasks: principalLookup{
		task: &models.Task{
			ID:          "automation-task",
			WorkspaceID: "workspace-1",
			Origin:      models.TaskOriginAutomationRun,
			Metadata:    map[string]interface{}{"automation_id": "automation-1"},
		},
		workspace: &models.Workspace{ID: "workspace-1"},
		session: &models.TaskSession{
			ID: "session-1", TaskID: "automation-task", State: models.TaskSessionStateRunning,
		},
	}}

	ctx, err := resolver.ScopePrincipal(context.Background(), "automation-task", "session-1")
	require.NoError(t, err)

	principal, ok := PrincipalFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, Principal{
		AutomationID:    "automation-1",
		WorkspaceID:     "workspace-1",
		CallerTaskID:    "automation-task",
		CallerSessionID: "session-1",
		Surface:         mcpprofile.SurfaceAutomation,
	}, principal)
	require.True(t, principal.IsAutomation())
}

func TestScopePrincipalRejectsSessionFromAnotherTask(t *testing.T) {
	resolver := &Resolver{tasks: principalLookup{
		task:      &models.Task{ID: "automation-task", WorkspaceID: "workspace-1"},
		workspace: &models.Workspace{ID: "workspace-1"},
		session:   &models.TaskSession{ID: "session-1", TaskID: "other-task"},
	}}

	_, err := resolver.ScopePrincipal(context.Background(), "automation-task", "session-1")
	require.Error(t, err)
}

// @covers AC-FR-03
func TestVerifyLiveCallerAcceptsOnlyLiveTaskSessions(t *testing.T) {
	for _, state := range models.AllTaskSessionStates {
		t.Run(string(state), func(t *testing.T) {
			resolver := &Resolver{tasks: principalLookup{
				task:      &models.Task{ID: "caller-task", WorkspaceID: "workspace-1"},
				workspace: &models.Workspace{ID: "workspace-1"},
				session: &models.TaskSession{
					ID: "caller-session", TaskID: "caller-task", State: state,
				},
			}}
			ctx := WithPrincipal(context.Background(), Principal{
				WorkspaceID: "workspace-1", CallerTaskID: "caller-task", CallerSessionID: "caller-session",
			})

			principal, err := resolver.VerifyLiveCaller(ctx)
			if state == models.TaskSessionStateStarting || state == models.TaskSessionStateRunning {
				require.NoError(t, err)
				require.Equal(t, "caller-task", principal.CallerTaskID)
				return
			}
			require.Error(t, err)
		})
	}
}

// @covers AC-FR-03
func TestVerifyLiveCallerRejectsMismatchedOrUnreadablePrincipal(t *testing.T) {
	resolver := &Resolver{tasks: principalLookup{
		task:      &models.Task{ID: "caller-task", WorkspaceID: "workspace-1"},
		workspace: &models.Workspace{ID: "workspace-1"},
		session: &models.TaskSession{
			ID: "caller-session", TaskID: "other-task", State: models.TaskSessionStateRunning,
		},
	}}

	ctx := WithPrincipal(context.Background(), Principal{
		WorkspaceID: "workspace-1", CallerTaskID: "caller-task", CallerSessionID: "caller-session",
	})
	_, err := resolver.VerifyLiveCaller(ctx)
	require.Error(t, err)

	_, err = resolver.VerifyLiveCaller(context.Background())
	require.Error(t, err)
}

// @covers AC-FR-03
func TestVerifyLiveCallerRejectsPrincipalFromAnotherWorkspace(t *testing.T) {
	resolver := &Resolver{tasks: principalLookup{
		task:      &models.Task{ID: "caller-task", WorkspaceID: "workspace-1"},
		workspace: &models.Workspace{ID: "workspace-1"},
		session: &models.TaskSession{
			ID: "caller-session", TaskID: "caller-task", State: models.TaskSessionStateRunning,
		},
	}}
	ctx := WithPrincipal(context.Background(), Principal{
		WorkspaceID: "workspace-2", CallerTaskID: "caller-task", CallerSessionID: "caller-session",
	})

	_, err := resolver.VerifyLiveCaller(ctx)
	require.Error(t, err)
}
