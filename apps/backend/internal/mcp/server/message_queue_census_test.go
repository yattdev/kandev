package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	mcpprofile "github.com/kandev/kandev/internal/mcp/profile"
	ws "github.com/kandev/kandev/pkg/websocket"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

func TestMessageQueueToolsRegisteredAndStrictBeforeDispatch(t *testing.T) {
	backend := &testBackend{response: map[string]interface{}{"count": 0, "entries": []interface{}{}}}
	s := newTaskModeServer(t, backend, "task-current")
	for _, name := range []string{messageQueueCensusTool, removeClaimedQueueTool} {
		_, ok := s.mcpServer.ListTools()[name]
		require.True(t, ok, name)
	}
	result := callTool(t, s, messageQueueCensusTool, map[string]interface{}{})
	require.False(t, result.IsError)
	require.Equal(t, ws.ActionMCPMessageQueueCensus, backend.lastAction)
	backend.lastAction = ""
	result = callTool(t, s, messageQueueCensusTool, map[string]interface{}{"task_id": "other"})
	require.True(t, result.IsError)
	require.Empty(t, backend.lastAction)
	result = callTool(t, s, removeClaimedQueueTool, map[string]interface{}{
		"entry_id": "entry-a", "claim": strings.Repeat("a", 64),
	})
	require.False(t, result.IsError)
	require.Equal(t, ws.ActionMCPRemoveClaimedQueueEntry, backend.lastAction)
	backend.lastAction = ""
	result = callTool(t, s, removeClaimedQueueTool, map[string]interface{}{
		"entry_id": "entry-a", "claim": strings.Repeat("A", 64),
	})
	require.True(t, result.IsError)
	require.Empty(t, backend.lastAction)
}

func TestMessageQueueToolsAbsentFromNonTaskSurfaces(t *testing.T) {
	backend := &testBackend{}
	for _, mode := range []string{ModeConfig, ModeExternal, ModeAutomation, ModeCoordinator} {
		t.Run(mode, func(t *testing.T) {
			s := New(backend, "test-session", "task-current", 10005, newTestLogger(t), "", false, mode)
			for _, name := range []string{messageQueueCensusTool, removeClaimedQueueTool} {
				require.NotContains(t, s.mcpServer.ListTools(), name)
			}
		})
	}
	managed := NewWithProfile(backend, "test-session", "task-current", 10005, newTestLogger(t), "", false,
		mcpprofile.New(mcpprofile.SurfaceManagedConversation, nil, nil))
	for _, name := range []string{messageQueueCensusTool, removeClaimedQueueTool} {
		require.NotContains(t, managed.mcpServer.ListTools(), name)
	}
}

func TestMessageQueueCensusFinalMCPResultBoundary(t *testing.T) {
	backend := &testBackend{}
	s := newTaskModeServer(t, backend, "task-current")
	for _, filler := range []string{"x", "é", `\"`} {
		resultBytes := func(n int) int {
			payload, err := json.Marshal(map[string]interface{}{"padding": strings.Repeat(filler, n)})
			require.NoError(t, err)
			encoded, err := json.Marshal(mcp.NewToolResultText(string(payload)))
			require.NoError(t, err)
			return len(encoded)
		}
		low, high := 0, queueResultLimit
		for low < high {
			mid := (low + high + 1) / 2
			if resultBytes(mid) <= queueResultLimit {
				low = mid
			} else {
				high = mid - 1
			}
		}
		if filler == "x" {
			require.Equal(t, queueResultLimit, resultBytes(low))
		}
		for _, n := range []int{low, low + 1} {
			backend.response = map[string]interface{}{"padding": strings.Repeat(filler, n)}
			result := callTool(t, s, messageQueueCensusTool, map[string]interface{}{})
			actual, err := json.Marshal(result)
			require.NoError(t, err)
			require.LessOrEqual(t, len(actual), queueResultLimit)
			require.Equal(t, resultBytes(n) > queueResultLimit, result.IsError)
		}
	}
}

func TestMessageQueueCensusOmittedArgumentsRegisteredDispatch(t *testing.T) {
	backend := &testBackend{response: map[string]interface{}{"count": 0}}
	s := newTaskModeServer(t, backend, "task-current")
	tool := s.mcpServer.ListTools()[messageQueueCensusTool]
	req := mcp.CallToolRequest{}
	req.Params.Name = messageQueueCensusTool
	result, err := tool.Handler(context.Background(), req)
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t, ws.ActionMCPMessageQueueCensus, backend.lastAction)
}

func TestMessageQueueCensusWireNullRefusedBeforeBackend(t *testing.T) {
	backend := &testBackend{response: map[string]interface{}{"count": 0}}
	s := newTaskModeServer(t, backend, "task-current")
	message := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_message_queue_census_kandev","arguments":null}}`)
	response := s.mcpServer.HandleMessage(context.Background(), message)
	require.NotNil(t, response)
	require.Empty(t, backend.lastAction, "explicit null application arguments must be refused before queue access")
	encoded, err := json.Marshal(response)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"isError":true`)
	for _, args := range []string{"", `,"arguments":{}`} {
		backend.lastAction = ""
		message = []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_message_queue_census_kandev"` + args + `}}`)
		response = s.mcpServer.HandleMessage(context.Background(), message)
		require.NotNil(t, response)
		require.Equal(t, ws.ActionMCPMessageQueueCensus, backend.lastAction)
	}
}

func TestMessageQueueRemovalWireArgumentByteBoundary(t *testing.T) {
	backend := &testBackend{response: map[string]interface{}{"removed": true}}
	s := newTaskModeServer(t, backend, "task-current")
	gin.SetMode(gin.TestMode)
	router := gin.New()
	s.RegisterBackendRoutes(router)
	server := httptest.NewServer(router)
	defer server.Close()
	init := postJSONRPC(t, server.URL+"/mcp", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0.0"}}}`, "")
	require.Equal(t, http.StatusOK, init.statusCode)
	base := fmt.Sprintf(`{"entry_id":"a","claim":"%s"}`, strings.Repeat("a", 64))
	for _, size := range []int{2048, 2049} {
		arguments := base[:16] + strings.Repeat(" ", size-len(base)) + base[16:]
		require.Len(t, []byte(arguments), size)
		for _, request := range []string{
			`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"remove_message_queue_entry_kandev","arguments":` + arguments + `}}`,
			`{"params":{"arguments":` + arguments + `,"name":"remove_message_queue_entry_kandev"},"method":"tools/call","id":2,"jsonrpc":"2.0"}`,
		} {
			backend.lastAction = ""
			response := postJSONRPC(t, server.URL+"/mcp", request, init.sessionID)
			require.Equal(t, http.StatusOK, response.statusCode)
			if size == 2048 {
				require.Equal(t, ws.ActionMCPRemoveClaimedQueueEntry, backend.lastAction)
			} else {
				require.Empty(t, backend.lastAction)
				require.Contains(t, response.body, `"isError":true`)
			}
		}
	}
	backend.lastAction = ""
	duplicate := `{"entry_id":"a","entry_id":"a","claim":"` + strings.Repeat("a", 64) + `"}`
	response := postJSONRPC(t, server.URL+"/mcp", `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"remove_message_queue_entry_kandev","arguments":`+duplicate+`}}`, init.sessionID)
	require.Equal(t, http.StatusOK, response.statusCode)
	require.Empty(t, backend.lastAction)
	require.Contains(t, response.body, `"isError":true`)
}
