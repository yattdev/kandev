package mcp

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLinkTaskPRByURLToolForwardsBoundCaller(t *testing.T) {
	backend := &testBackend{response: map[string]interface{}{"state_known": true}}
	s := newTaskModeServer(t, backend, "parent-task")
	tool, ok := s.mcpServer.ListTools()["link_task_pr_by_url_kandev"]
	require.True(t, ok)
	var schema map[string]interface{}
	require.NoError(t, json.Unmarshal(tool.Tool.RawInputSchema, &schema))
	require.Equal(t, false, schema["additionalProperties"])
	args := map[string]interface{}{
		"task_id": "child-task", "repository_id": "fork-repository",
		"pr_url": "https://github.com/kdlbs/kandev/pull/3166",
	}
	result := callTool(t, s, "link_task_pr_by_url_kandev", args)
	require.False(t, result.IsError)
	require.Equal(t, "mcp.link_task_pr_by_url", backend.lastAction)
	payload, ok := backend.lastPayload.(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, "parent-task", payload["caller_task_id"])
	require.Equal(t, args["pr_url"], payload["pr_url"])
}
