package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/kandev/kandev/internal/common/logger"
	mcpprofile "github.com/kandev/kandev/internal/mcp/profile"
	"github.com/kandev/kandev/internal/orchestrator/messagequeue"
	ws "github.com/kandev/kandev/pkg/websocket"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type censusBackend struct {
	called int
	action string
}

func (b *censusBackend) RequestPayload(_ context.Context, action string, payload, result interface{}) error {
	b.called++
	b.action = action
	*result.(*messagequeue.ActiveQueueCensus) = messagequeue.ActiveQueueCensus{
		Version: 1, TaskID: "task", SessionID: "session", SessionIncarnationID: "incarnation",
		SnapshotRevisionSHA256: "revision", Complete: true, Count: 0, Capacity: 10,
		Entries: []messagequeue.ActiveQueueCensusEntry{},
	}
	return nil
}

// @covers AC-TASKS-ACTIVE-QUEUE-CENSUS-001.3
func TestActiveQueueCensusToolOnlyOnTaskSurfaces(t *testing.T) {
	for _, tc := range []struct {
		name    string
		surface mcpprofile.Surface
		want    bool
	}{
		{"kanban", mcpprofile.SurfaceKanbanTask, true},
		{"office", mcpprofile.SurfaceOfficeTask, true},
		{"external", mcpprofile.SurfaceExternal, false},
		{"configuration", mcpprofile.SurfaceConfiguration, false},
		{"automation", mcpprofile.SurfaceAutomation, false},
		{"coordinator", mcpprofile.SurfaceCoordinator, false},
		{"managed", mcpprofile.SurfaceManagedConversation, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewWithProfile(&testBackend{}, "session", "task", 10005, newTestLogger(t), "", false,
				mcpprofile.New(tc.surface, nil, nil))
			_, got := s.mcpServer.ListTools()["get_message_queue_census_kandev"]
			require.Equal(t, tc.want, got)
		})
	}
}

// @covers AC-TASKS-ACTIVE-QUEUE-CENSUS-001.1
func TestActiveQueueCensusToolForwardsEmptyRequest(t *testing.T) {
	backend := &censusBackend{}
	s := NewWithProfile(backend, "session", "task", 10005, newTestLogger(t), "", false,
		mcpprofile.New(mcpprofile.SurfaceKanbanTask, nil, nil))
	tool := s.mcpServer.ListTools()[messageQueueCensusToolName]
	require.NotNil(t, tool)
	request := mcp.CallToolRequest{Params: mcp.CallToolParams{Name: messageQueueCensusToolName, Arguments: map[string]any{}}}
	result, err := tool.Handler(context.Background(), request)
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t, ws.ActionMCPGetMessageQueueCensus, backend.action)
	require.Equal(t, 1, backend.called)
	text, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	require.JSONEq(t, `{"version":1,"task_id":"task","session_id":"session","session_incarnation_id":"incarnation","snapshot_revision_sha256":"revision","complete":true,"count":0,"capacity":10,"entries":[]}`, text.Text)

	request.Params.Arguments = map[string]any{"task_id": "another-task"}
	result, err = tool.Handler(context.Background(), request)
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Equal(t, 1, backend.called)
}

// @covers AC-TASKS-ACTIVE-QUEUE-CENSUS-001.1 AC-TASKS-ACTIVE-QUEUE-CENSUS-001.6
func TestActiveQueueCensusRejectsNonObjectArgumentsWithoutLoggingValues(t *testing.T) {
	core, observed := observer.New(zap.DebugLevel)
	log, err := logger.NewFromZap(zap.New(core))
	require.NoError(t, err)
	backend := &censusBackend{}
	s := NewWithProfile(backend, "session", "task", 10005, log, "", false,
		mcpprofile.New(mcpprofile.SurfaceKanbanTask, nil, nil))
	tool := s.mcpServer.ListTools()[messageQueueCensusToolName]
	for _, arguments := range []any{nil, json.RawMessage(`null`), json.RawMessage(`[]`), json.RawMessage(`{"x":1,"x":2}`), map[string]any{"content": "private body"}} {
		request := mcp.CallToolRequest{Params: mcp.CallToolParams{Name: messageQueueCensusToolName, Arguments: arguments}}
		result, err := tool.Handler(context.Background(), request)
		require.NoError(t, err)
		require.True(t, result.IsError, "%v", arguments)
	}
	require.Equal(t, 0, backend.called)
	require.NotContains(t, fmt.Sprint(observed.All()), "private body")
	valid := mcp.CallToolRequest{Params: mcp.CallToolParams{Name: messageQueueCensusToolName, Arguments: json.RawMessage(`{}`)}}
	result, err := tool.Handler(context.Background(), valid)
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t, 1, backend.called)
}
