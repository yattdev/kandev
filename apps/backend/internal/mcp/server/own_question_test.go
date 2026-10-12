package mcp

import (
	"testing"

	ws "github.com/kandev/kandev/pkg/websocket"
	"github.com/stretchr/testify/require"
)

func TestOwnQuestionReceiptSchemaForwardingAndStatus(t *testing.T) {
	backend := &testBackend{response: map[string]interface{}{"status": "pending", "pending_id": "synthetic-pending", "retry_allowed": false, "questions": []any{}}}
	s := newTaskModeServer(t, backend, "own-task")
	require.Contains(t, s.mcpServer.ListTools(), "get_own_question_status_kandev")
	result := callTool(t, s, "ask_user_question_kandev", map[string]interface{}{"return_mode": "receipt", "questions": []map[string]interface{}{{"id": "stable-q", "prompt": "Synthetic choice?", "options": []map[string]interface{}{{"label": "A", "description": "First"}, {"label": "B", "description": "Second"}}}}})
	require.False(t, result.IsError)
	require.NotNil(t, result.StructuredContent)
	require.Equal(t, ws.ActionMCPAskUserQuestion, backend.lastAction)
	require.Equal(t, "receipt", backend.lastPayload.(map[string]interface{})["return_mode"])
	require.Equal(t, "own-task", backend.lastPayload.(map[string]interface{})["task_id"])
	result = callTool(t, s, "get_own_question_status_kandev", map[string]interface{}{"question_ids": []string{"stable-q"}})
	require.False(t, result.IsError)
	require.Equal(t, ws.ActionMCPGetOwnQuestionStatus, backend.lastAction)
	require.Equal(t, "own-task", backend.lastPayload.(map[string]interface{})["task_id"])
	require.Equal(t, s.sessionID, backend.lastPayload.(map[string]interface{})["session_id"])
	result = callTool(t, s, "get_own_question_status_kandev", map[string]interface{}{"question_ids": []string{"stable-q"}, "task_id": "foreign", "session_id": "foreign"})
	require.True(t, result.IsError)
}

func TestOwnQuestionReceiptRequiresExplicitIDs(t *testing.T) {
	backend := &testBackend{}
	s := newTaskModeServer(t, backend, "own-task")
	result := callTool(t, s, "ask_user_question_kandev", map[string]interface{}{"return_mode": "receipt", "questions": []map[string]interface{}{{"prompt": "Synthetic?", "options": []map[string]interface{}{{"label": "A", "description": "First"}, {"label": "B", "description": "Second"}}}}})
	require.True(t, result.IsError)
	require.Empty(t, backend.lastAction)
}

func TestOwnQuestionReadbackDoesNotExpandRestrictedCatalogs(t *testing.T) {
	for _, s := range []*Server{
		New(&testBackend{}, "s", "t", 10005, newTestLogger(t), "", false, ModeExternal),
		New(&testBackend{}, "s", "t", 10005, newTestLogger(t), "", true, ModeTask),
	} {
		require.NotContains(t, s.mcpServer.ListTools(), "get_own_question_status_kandev")
	}
}
