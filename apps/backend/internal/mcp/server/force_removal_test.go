package mcp

import (
	"testing"

	ws "github.com/kandev/kandev/pkg/websocket"
	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCommitForceRemovalToolForwardsAdmissionWithoutCallerIdentity(t *testing.T) {
	backend := &testBackend{response: map[string]interface{}{"replay": false, "task_id": "target-task"}}
	s := New(backend, "caller-session", "caller-task", 10005, newTestLogger(t), "", false, ModeTask)

	result := callTool(t, s, "commit_force_removal_kandev", map[string]interface{}{
		forceRemovalTaskIDArg:                 " target-task ",
		forceRemovalWorkspaceIDArg:            " target-workspace ",
		forceRemovalExpectedTaskGenerationArg: " target-generation ",
		forceRemovalAdmissionGenerationArg:    " admission-generation ",
		forceRemovalOperationIDArg:            " operation-id ",
		forceRemovalRequestDigestArg:          " request-digest ",
		forceRemovalPreviewDigestArg:          " preview-digest ",
		forceRemovalGrantIDArg:                " grant-id ",
	})

	require.False(t, result.IsError)
	assert.Equal(t, ws.ActionMCPCommitForceRemoval, backend.lastAction)
	assert.Equal(t, map[string]interface{}{
		forceRemovalTaskIDArg:                 "target-task",
		forceRemovalWorkspaceIDArg:            "target-workspace",
		forceRemovalExpectedTaskGenerationArg: "target-generation",
		forceRemovalAdmissionGenerationArg:    "admission-generation",
		forceRemovalOperationIDArg:            "operation-id",
		forceRemovalRequestDigestArg:          "request-digest",
		forceRemovalPreviewDigestArg:          "preview-digest",
		forceRemovalGrantIDArg:                "grant-id",
	}, backend.lastPayload)
	assert.NotContains(t, backend.lastPayload, "caller_task_id")
	assert.NotContains(t, backend.lastPayload, "caller_session_id")
}

func TestCommitForceRemovalToolRequiresBoundCallerSession(t *testing.T) {
	s := New(&testBackend{}, "", "", 10005, newTestLogger(t), "", false, ModeTask)

	result := callTool(t, s, "commit_force_removal_kandev", map[string]interface{}{
		forceRemovalTaskIDArg:                 "target-task",
		forceRemovalWorkspaceIDArg:            "target-workspace",
		forceRemovalExpectedTaskGenerationArg: "target-generation",
		forceRemovalAdmissionGenerationArg:    "admission-generation",
		forceRemovalOperationIDArg:            "operation-id",
		forceRemovalRequestDigestArg:          "request-digest",
		forceRemovalPreviewDigestArg:          "preview-digest",
		forceRemovalGrantIDArg:                "grant-id",
	})

	require.True(t, result.IsError)
	text, ok := result.Content[0].(mcplib.TextContent)
	require.True(t, ok)
	assert.Contains(t, text.Text, "requires a bound task and session")
}

func TestCommitForceRemovalToolMetadata(t *testing.T) {
	s := newTaskModeServer(t, &testBackend{}, "caller-task")
	tool, ok := s.mcpServer.ListTools()["commit_force_removal_kandev"]
	require.True(t, ok)
	assert.True(t, *tool.Tool.Annotations.DestructiveHint)
	assert.False(t, *tool.Tool.Annotations.IdempotentHint)
	assert.False(t, *tool.Tool.Annotations.ReadOnlyHint)
	assert.False(t, *tool.Tool.Annotations.OpenWorldHint)
	properties := toolInputProperties(t, s, "commit_force_removal_kandev")
	assert.NotContains(t, properties, "caller_task_id")
	assert.NotContains(t, properties, "caller_session_id")
}
