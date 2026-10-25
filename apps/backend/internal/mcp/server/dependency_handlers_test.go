package mcp

import (
	mcpprofile "github.com/kandev/kandev/internal/mcp/profile"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ws "github.com/kandev/kandev/pkg/websocket"
)

func TestTaskBoundDependencyMutationRejectsPeerDependent(t *testing.T) {
	for _, tc := range []struct {
		name   string
		action string
	}{
		{name: "add", action: "add_task_dependency_kandev"},
		{name: "remove", action: "remove_task_dependency_kandev"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := &testBackend{}
			s := newTaskModeServer(t, backend, "caller-task")
			result := callTool(t, s, tc.action, map[string]interface{}{
				"task_id":            "peer-task",
				"depends_on_task_id": "predecessor-task",
			})
			require.True(t, result.IsError)
			assert.Empty(t, backend.lastAction, "peer mutation must stop before backend dispatch")
		})
	}
}

func TestTaskBoundDependencyMutationPreservesSelfTarget(t *testing.T) {
	backend := &testBackend{response: map[string]interface{}{"task_id": "caller-task"}}
	s := newTaskModeServer(t, backend, "caller-task")
	result := callTool(t, s, "add_task_dependency_kandev", map[string]interface{}{
		"task_id":            "self",
		"depends_on_task_id": "predecessor-task",
	})
	require.False(t, result.IsError)
	assert.Equal(t, ws.ActionMCPAddTaskDependency, backend.lastAction)
	payload, ok := backend.lastPayload.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "caller-task", payload["task_id"])
	assert.Equal(t, "predecessor-task", payload["depends_on_task_id"])
}

func TestCoordinatorDependencySchemaRejectionIsAuditedWithoutTargetValues(t *testing.T) {
	for _, tc := range []struct {
		name, tool, operation  string
		args                   map[string]interface{}
		dependent, predecessor bool
	}{
		{"missing", "add_task_dependency_kandev", "add", map[string]interface{}{"task_id": "private-task"}, true, false},
		{"wrong type", "remove_task_dependency_kandev", "remove", map[string]interface{}{"task_id": 42, "depends_on_task_id": "private-predecessor"}, true, true},
		{"unknown field", "add_task_dependency_kandev", "add", map[string]interface{}{"task_id": "private-task", "depends_on_task_id": "private-predecessor", "extra": "private-value"}, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := &testBackend{response: map[string]interface{}{"audited": true}}
			s := newCoordinatorDependencyTestServer(t, backend)
			result := callTool(t, s, tc.tool, tc.args)
			require.True(t, result.IsError)
			require.Equal(t, ws.ActionMCPAuditTaskDependencyAttempt, backend.lastAction)
			payload, ok := backend.lastPayload.(map[string]any)
			require.True(t, ok)
			require.Equal(t, map[string]any{
				"operation":              tc.operation,
				"dependent_id_present":   tc.dependent,
				"predecessor_id_present": tc.predecessor,
			}, payload)
		})
	}
}

func TestCoordinatorDependencySchemaRejectionFailsClosedWhenAuditFails(t *testing.T) {
	backend := &testBackend{}
	s := newCoordinatorDependencyTestServer(t, backend)
	result := callTool(t, s, "add_task_dependency_kandev", map[string]interface{}{"task_id": "private-task"})
	require.True(t, result.IsError)
	require.Equal(t, ws.ActionMCPAuditTaskDependencyAttempt, backend.lastAction)
}

func newCoordinatorDependencyTestServer(t *testing.T, backend BackendClient) *Server {
	t.Helper()
	profile := mcpprofile.NewCoordinator()
	profile.CoordinatorToolPolicy = &mcpprofile.CoordinatorToolPolicy{
		Version: 1, CoordinatorID: "coordinator", WorkspaceID: "workspace",
		ConversationTaskID: "coordinator-task",
		ToolNames:          []string{"add_task_dependency_kandev", "remove_task_dependency_kandev"},
	}
	return NewWithProfile(backend, "session", "coordinator-task", 10005, newTestLogger(t), "", false, profile)
}
