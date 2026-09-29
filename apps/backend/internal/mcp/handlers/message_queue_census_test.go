package handlers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kandev/kandev/internal/mcp/scope"
	"github.com/kandev/kandev/internal/orchestrator/messagequeue"
	ws "github.com/kandev/kandev/pkg/websocket"
	"github.com/stretchr/testify/require"
)

func TestGetMessageQueueCensusDispatchesOnlyForCallingSession(t *testing.T) {
	queue := messagequeue.NewServiceMemory(testLogger(t))
	_, err := queue.QueueMessage(context.Background(), "session-1", "task-1", "private prompt", "", messagequeue.QueuedByAgent, false, nil)
	require.NoError(t, err)

	h := &Handlers{messageQueue: queue, logger: testLogger(t)}
	dispatcher := ws.NewDispatcher()
	h.RegisterHandlers(dispatcher)
	ctx := scope.WithPrincipal(context.Background(), scope.Principal{
		WorkspaceID: "workspace-1", CallerTaskID: "task-1", CallerSessionID: "session-1",
	})
	response, err := dispatcher.Dispatch(ctx, makeWSMessage(t, "mcp.get_message_queue_census", map[string]string{
		"task_id": "task-1", "session_id": "session-1",
	}))
	require.NoError(t, err)
	require.Equal(t, ws.MessageTypeResponse, response.Type)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(response.Payload, &payload))
	require.Equal(t, float64(1), payload["count"])
	encoded, err := json.Marshal(payload)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "private prompt")

	response, err = dispatcher.Dispatch(ctx, makeWSMessage(t, "mcp.get_message_queue_census", map[string]string{
		"task_id": "task-2", "session_id": "session-1",
	}))
	require.NoError(t, err)
	assertWSError(t, response, ws.ErrorCodeForbidden)
	if strings.Contains(string(response.Payload), "private prompt") {
		t.Fatal("forbidden response leaked queue content")
	}
}

func TestRemoveMessageQueueEntryPreservesOtherSessionEntries(t *testing.T) {
	queue := messagequeue.NewServiceMemory(testLogger(t))
	remove, err := queue.QueueMessage(context.Background(), "session-1", "task-1", "remove", "", messagequeue.QueuedByAgent, false, nil)
	require.NoError(t, err)
	keep, err := queue.QueueMessage(context.Background(), "session-2", "task-2", "keep", "", messagequeue.QueuedByAgent, false, nil)
	require.NoError(t, err)
	h := &Handlers{messageQueue: queue, logger: testLogger(t)}
	dispatcher := ws.NewDispatcher()
	h.RegisterHandlers(dispatcher)
	ctx := scope.WithPrincipal(context.Background(), scope.Principal{WorkspaceID: "workspace-1", CallerTaskID: "task-1", CallerSessionID: "session-1"})
	identity, err := queue.ResolveSessionIdentity(context.Background(), "task-1", "session-1")
	require.NoError(t, err)
	firstStatus, err := queue.Snapshot(context.Background(), identity)
	require.NoError(t, err)
	response, err := dispatcher.Dispatch(ctx, makeWSMessage(t, "mcp.remove_message_queue_entry", map[string]string{"task_id": "task-1", "session_id": "session-1", "entry_id": remove.ID, "claim": messagequeue.QueueEntryClaim(&firstStatus.Entries[0])}))
	require.NoError(t, err)
	require.Equal(t, ws.MessageTypeResponse, response.Type)
	identity, err = queue.ResolveSessionIdentity(context.Background(), "task-2", "session-2")
	require.NoError(t, err)
	status, err := queue.Snapshot(context.Background(), identity)
	require.NoError(t, err)
	require.Len(t, status.Entries, 1)
	require.Equal(t, keep.ID, status.Entries[0].ID)

	response, err = dispatcher.Dispatch(ctx, makeWSMessage(t, "mcp.remove_message_queue_entry", map[string]string{"task_id": "task-1", "session_id": "session-1", "entry_id": keep.ID, "claim": "foreign"}))
	require.NoError(t, err)
	assertWSError(t, response, ws.ErrorCodeNotFound)
}

func TestRemoveMessageQueueEntryRejectsStaleClaim(t *testing.T) {
	queue := messagequeue.NewServiceMemory(testLogger(t))
	entry, err := queue.QueueMessage(context.Background(), "session-1", "task-1", "keep", "", messagequeue.QueuedByAgent, false, nil)
	require.NoError(t, err)
	h := &Handlers{messageQueue: queue, logger: testLogger(t)}
	dispatcher := ws.NewDispatcher()
	h.RegisterHandlers(dispatcher)
	ctx := scope.WithPrincipal(context.Background(), scope.Principal{WorkspaceID: "workspace-1", CallerTaskID: "task-1", CallerSessionID: "session-1"})
	response, err := dispatcher.Dispatch(ctx, makeWSMessage(t, "mcp.remove_message_queue_entry", map[string]string{
		"task_id": "task-1", "session_id": "session-1", "entry_id": entry.ID, "claim": "stale",
	}))
	require.NoError(t, err)
	assertWSError(t, response, ws.ErrorCodeConflict)
	identity, err := queue.ResolveSessionIdentity(context.Background(), "task-1", "session-1")
	require.NoError(t, err)
	status, err := queue.Snapshot(context.Background(), identity)
	require.NoError(t, err)
	require.Len(t, status.Entries, 1)
}
