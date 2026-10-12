package mcp

import (
	ws "github.com/kandev/kandev/pkg/websocket"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestTaskMessageReceiptToolScopeAndForwarding(t *testing.T) {
	backend := &testBackend{response: map[string]interface{}{"status": "queued", "message_id": "original-message", "retry_allowed": false, "continue_allowed": true, "continuation": "recheck_original_queue"}}
	s := newTaskModeServer(t, backend, "sender-task")
	require.Contains(t, s.mcpServer.ListTools(), "get_task_message_status_kandev")
	result := callTool(t, s, "message_task_kandev", map[string]interface{}{"task_id": "target", "session_id": "target-session", "intent_id": "stable-intent-1", "prompt": "synthetic", "delivery_mode": "queued"})
	require.False(t, result.IsError)
	require.NotNil(t, result.StructuredContent)
	payload := backend.lastPayload.(map[string]interface{})
	require.Equal(t, "sender-task", payload["sender_task_id"])
	require.Equal(t, s.sessionID, payload["sender_session_id"])
	require.Equal(t, "stable-intent-1", payload["intent_id"])
	for _, entry := range []struct{ name, action string }{{"get_task_message_status_kandev", ws.ActionMCPGetTaskMessageStatus}, {"continue_task_message_kandev", ws.ActionMCPContinueTaskMessage}} {
		result = callTool(t, s, entry.name, map[string]interface{}{"message_id": "legacy-message"})
		require.False(t, result.IsError)
		require.Equal(t, entry.action, backend.lastAction)
		require.NotNil(t, result.StructuredContent)
		result = callTool(t, s, entry.name, map[string]interface{}{"message_id": "legacy-message", "sender_task_id": "peer"})
		require.True(t, result.IsError)
	}
	for _, mode := range []string{ModeExternal, ModeAutomation} {
		restricted := New(&testBackend{}, "s", "t", 10006, newTestLogger(t), "", false, mode)
		require.NotContains(t, restricted.mcpServer.ListTools(), "get_task_message_status_kandev")
		require.NotContains(t, restricted.mcpServer.ListTools(), "continue_task_message_kandev")
	}
}
