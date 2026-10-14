package mcp

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOwnAnswerUnsupportedFormatRemainsStructuredError(t *testing.T) {
	b := &testBackend{response: map[string]interface{}{
		"status": "not_reconciled", "reason_code": "ANSWER_FORMAT_UNSUPPORTED",
		"retry_allowed": false, "changed": false,
	}}
	s := newTaskModeServer(t, b, "own-task")
	args := map[string]interface{}{"question_id": "stable-q", "pending_id": "pending", "question_message_id": "question", "question_sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", "answer_message_id": "human", "answer_sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}
	result := callTool(t, s, ownAnswerToolName, args)
	require.True(t, result.IsError, "unsupported reconciliation must not look successful")
	require.Equal(t, "explicit_disposition_v1", b.lastPayload.(map[string]any)["result_mode"])
	require.NotNil(t, result.StructuredContent)
	raw, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	require.Contains(t, string(raw), "ANSWER_FORMAT_UNSUPPORTED")
}

func TestOwnAnswerUnexpectedErrorIsStillRedacted(t *testing.T) {
	b := &testBackend{err: errors.New("private database failure with sensitive contents")}
	s := newTaskModeServer(t, b, "own-task")
	args := map[string]interface{}{"question_id": "stable-q", "pending_id": "pending", "question_message_id": "question", "question_sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", "answer_message_id": "human", "answer_sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}
	result := callTool(t, s, ownAnswerToolName, args)
	require.True(t, result.IsError)
	raw, err := json.Marshal(result)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "private database failure")
}
