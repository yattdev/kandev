package mcp

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestOwnAnswerReconciliationBoundInput(t *testing.T) {
	b := &testBackend{response: map[string]interface{}{"status": "answered"}}
	s := newTaskModeServer(t, b, "own-task")
	require.Contains(t, s.mcpServer.ListTools(), "reconcile_own_question_answer_kandev")
	args := map[string]interface{}{"question_id": "stable-q", "pending_id": "pending", "question_message_id": "question-message", "question_sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", "answer_message_id": "human-message", "answer_sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}
	result := callTool(t, s, "reconcile_own_question_answer_kandev", args)
	require.False(t, result.IsError)
	require.Equal(t, "own-task", b.lastPayload.(map[string]interface{})["task_id"])
	require.Equal(t, s.sessionID, b.lastPayload.(map[string]interface{})["session_id"])
	args["answer"] = "fabricated"
	require.True(t, callTool(t, s, "reconcile_own_question_answer_kandev", args).IsError)
}

func TestOwnAnswerRestrictedCatalogs(t *testing.T) {
	for _, s := range []*Server{New(&testBackend{}, "s", "t", 10005, newTestLogger(t), "", false, ModeConfig), New(&testBackend{}, "s", "t", 10005, newTestLogger(t), "", false, ModeOffice), New(&testBackend{}, "s", "t", 10005, newTestLogger(t), "", false, ModeExternal), New(&testBackend{}, "s", "t", 10005, newTestLogger(t), "", true, ModeTask)} {
		require.NotContains(t, s.mcpServer.ListTools(), ownAnswerToolName)
	}
}
