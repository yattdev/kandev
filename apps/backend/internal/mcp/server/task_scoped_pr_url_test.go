package mcp

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestManageTaskChangeRequestScopedPRURL(t *testing.T) {
	backend := &testBackend{response: map[string]interface{}{"state_known": true}}
	s := newTaskModeServer(t, backend, "parent")
	result := callTool(t, s, "manage_task_change_request_kandev", map[string]interface{}{
		"operation": "link", "task_id": "child", "provider": "github", "repository_id": "fork", "number": 42,
		"pr_url": "https://github.com/upstream/project/pull/42", "expected_head_sha": "0123456789012345678901234567890123456789",
	})
	require.False(t, result.IsError, "%+v", result)
	payload := backend.lastPayload.(map[string]interface{})
	require.Equal(t, "https://github.com/upstream/project/pull/42", payload["pr_url"])
	require.Equal(t, "0123456789012345678901234567890123456789", payload["expected_head_sha"])
}

func TestScopedPRURLSchemaRejectsModes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(map[string]interface{})
	}{
		{"missing head", func(a map[string]interface{}) { delete(a, "expected_head_sha") }}, {"missing URL", func(a map[string]interface{}) { delete(a, "pr_url") }},
		{"GitLab", func(a map[string]interface{}) { a["provider"] = "gitlab" }}, {"unlink", func(a map[string]interface{}) { a["operation"] = "unlink" }},
		{"nonhex head", func(a map[string]interface{}) { a["expected_head_sha"] = "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := &testBackend{}
			s := newTaskModeServer(t, backend, "parent")
			args := map[string]interface{}{"operation": "link", "task_id": "child", "provider": "github", "repository_id": "fork", "number": 42, "pr_url": "https://github.com/upstream/project/pull/42", "expected_head_sha": "0123456789012345678901234567890123456789"}
			tc.change(args)
			result := callTool(t, s, "manage_task_change_request_kandev", args)
			require.True(t, result.IsError)
			require.Empty(t, backend.lastAction)
		})
	}
}
