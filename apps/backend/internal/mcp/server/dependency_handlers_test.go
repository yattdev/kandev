package mcp

import (
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
