package scope

import (
	"context"
	"testing"

	streams "github.com/kandev/kandev/internal/agentctl/types/streams"
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
		session:   &models.TaskSession{ID: "session-1", TaskID: "automation-task", QueueIncarnationID: "incarnation-1"},
	}}

	ctx, err := resolver.ScopePrincipal(context.Background(), "automation-task", "session-1")
	require.NoError(t, err)

	principal, ok := PrincipalFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, Principal{
		AutomationID:        "automation-1",
		WorkspaceID:         "workspace-1",
		CallerTaskID:        "automation-task",
		CallerSessionID:     "session-1",
		CallerIncarnationID: "incarnation-1",
		Surface:             mcpprofile.SurfaceAutomation,
	}, principal)
	require.True(t, principal.IsAutomation())
}

func TestScopePrincipalBindsMatchingExecutionContext(t *testing.T) {
	resolver := &Resolver{tasks: principalLookup{
		task:      &models.Task{ID: "automation-task", WorkspaceID: "workspace-1"},
		workspace: &models.Workspace{ID: "workspace-1"},
		session:   &models.TaskSession{ID: "session-1", TaskID: "automation-task", QueueIncarnationID: "incarnation-1"},
	}}
	ctx := streams.WithMCPExecutionContext(context.Background(), streams.MCPExecutionContext{
		ExecutionID: "execution-1", TaskID: "automation-task", SessionID: "session-1",
	})

	scoped, err := resolver.ScopePrincipal(ctx, "automation-task", "session-1")
	require.NoError(t, err)
	principal, ok := PrincipalFromContext(scoped)
	require.True(t, ok)
	require.Equal(t, "execution-1", principal.ExecutionID)
}

func TestScopePrincipalKeepsOriginalExecutionScopeAfterReplacement(t *testing.T) {
	resolver := &Resolver{tasks: principalLookup{
		task:      &models.Task{ID: "task-1", WorkspaceID: "workspace-2"},
		workspace: &models.Workspace{ID: "workspace-2"},
		session:   &models.TaskSession{ID: "session-1", TaskID: "task-1", QueueIncarnationID: "incarnation-2"},
	}}
	ctx := streams.WithMCPExecutionContext(context.Background(), streams.MCPExecutionContext{
		ExecutionID: "execution-1", TaskID: "task-1", SessionID: "session-1",
		OriginalWorkspaceID: "workspace-1", OriginalQueueIncarnationID: "incarnation-1",
	})
	scoped, err := resolver.ScopePrincipal(ctx, "task-1", "session-1")
	require.NoError(t, err)
	principal, ok := PrincipalFromContext(scoped)
	require.True(t, ok)
	require.Equal(t, "incarnation-1", principal.OriginalQueueIncarnationID)
	require.Equal(t, "incarnation-2", principal.CallerIncarnationID)
	require.Equal(t, "workspace-1", principal.OriginalWorkspaceID)
	require.Equal(t, "workspace-2", principal.WorkspaceID)
}

func TestScopePrincipalRejectsMismatchedExecutionContext(t *testing.T) {
	resolver := &Resolver{tasks: principalLookup{
		task:      &models.Task{ID: "automation-task", WorkspaceID: "workspace-1"},
		workspace: &models.Workspace{ID: "workspace-1"},
		session:   &models.TaskSession{ID: "session-1", TaskID: "automation-task", QueueIncarnationID: "incarnation-1"},
	}}
	ctx := streams.WithMCPExecutionContext(context.Background(), streams.MCPExecutionContext{
		ExecutionID: "execution-1", TaskID: "other-task", SessionID: "session-1",
	})

	_, err := resolver.ScopePrincipal(ctx, "automation-task", "session-1")
	require.ErrorContains(t, err, "execution context does not match session")
}

func TestScopePrincipalKeepsLegacySessionAvailableToGenericTools(t *testing.T) {
	resolver := &Resolver{tasks: principalLookup{
		task:      &models.Task{ID: "automation-task", WorkspaceID: "workspace-1"},
		workspace: &models.Workspace{ID: "workspace-1"},
		session:   &models.TaskSession{ID: "session-1", TaskID: "automation-task"},
	}}

	ctx, err := resolver.ScopePrincipal(context.Background(), "automation-task", "session-1")
	require.NoError(t, err)
	principal, ok := PrincipalFromContext(ctx)
	require.True(t, ok)
	require.Empty(t, principal.CallerIncarnationID)
	require.Empty(t, principal.OriginalQueueIncarnationID)
}

func TestScopePrincipalRejectsSessionFromAnotherTask(t *testing.T) {
	resolver := &Resolver{tasks: principalLookup{
		task:      &models.Task{ID: "automation-task", WorkspaceID: "workspace-1"},
		workspace: &models.Workspace{ID: "workspace-1"},
		session:   &models.TaskSession{ID: "session-1", TaskID: "other-task", QueueIncarnationID: "incarnation-1"},
	}}

	_, err := resolver.ScopePrincipal(context.Background(), "automation-task", "session-1")
	require.Error(t, err)
}

// fakeCoordinatorLookup is a test double for CoordinatorLookup.
type fakeCoordinatorLookup struct {
	coordinatorID string
	ok            bool
	err           error
}

func (f fakeCoordinatorLookup) CoordinatorForConversationTask(context.Context, string) (string, bool, error) {
	return f.coordinatorID, f.ok, f.err
}

func coordinatorConversationLookup() principalLookup {
	return principalLookup{
		task: &models.Task{
			ID:          "conversation-task",
			WorkspaceID: "workspace-1",
			Origin:      models.TaskOriginCoordinator,
		},
		workspace: &models.Workspace{ID: "workspace-1"},
		session:   &models.TaskSession{ID: "session-1", TaskID: "conversation-task", QueueIncarnationID: "incarnation-1"},
	}
}

func TestScopePrincipalDerivesCoordinatorIdentityFromExecution(t *testing.T) {
	resolver := &Resolver{
		tasks:        coordinatorConversationLookup(),
		coordinators: fakeCoordinatorLookup{coordinatorID: "coordinator-1", ok: true},
	}

	ctx, err := resolver.ScopePrincipal(context.Background(), "conversation-task", "session-1")
	require.NoError(t, err)

	principal, ok := PrincipalFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, Principal{
		WorkspaceID:         "workspace-1",
		CallerTaskID:        "conversation-task",
		CallerSessionID:     "session-1",
		CallerIncarnationID: "incarnation-1",
		Surface:             mcpprofile.SurfaceCoordinator,
		CoordinatorID:       "coordinator-1",
	}, principal)
}

func TestScopePrincipalRefusesCoordinatorTaskWithNoLookupWired(t *testing.T) {
	resolver := &Resolver{tasks: coordinatorConversationLookup()}

	_, err := resolver.ScopePrincipal(context.Background(), "conversation-task", "session-1")
	require.Error(t, err)
}

func TestScopePrincipalRefusesOrphanedCoordinatorConversationTask(t *testing.T) {
	resolver := &Resolver{
		tasks:        coordinatorConversationLookup(),
		coordinators: fakeCoordinatorLookup{ok: false},
	}

	_, err := resolver.ScopePrincipal(context.Background(), "conversation-task", "session-1")
	require.Error(t, err)
}

func TestScopePrincipalRefusesOnCoordinatorLookupError(t *testing.T) {
	resolver := &Resolver{
		tasks:        coordinatorConversationLookup(),
		coordinators: fakeCoordinatorLookup{err: context.DeadlineExceeded},
	}

	_, err := resolver.ScopePrincipal(context.Background(), "conversation-task", "session-1")
	require.Error(t, err)
}
