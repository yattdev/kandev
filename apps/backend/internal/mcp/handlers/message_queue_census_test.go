package handlers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kandev/kandev/internal/auth/authn"
	mcpprofile "github.com/kandev/kandev/internal/mcp/profile"
	mcpscope "github.com/kandev/kandev/internal/mcp/scope"
	"github.com/kandev/kandev/internal/orchestrator/messagequeue"
	ws "github.com/kandev/kandev/pkg/websocket"
	"github.com/stretchr/testify/require"
)

type censusQueueSpy struct {
	reads, removals int
	identity        messagequeue.QueueSessionIdentity
}

func (q *censusQueueSpy) QueueMessage(context.Context, string, string, string, string, string, bool, []messagequeue.MessageAttachment) (*messagequeue.QueuedMessage, error) {
	return nil, nil
}
func (q *censusQueueSpy) SetPendingMove(context.Context, string, *messagequeue.PendingMove) error {
	return nil
}
func (q *censusQueueSpy) RemoveEntryForSession(context.Context, messagequeue.QueueSessionIdentity, string) (*messagequeue.QueueRemovalResult, error) {
	return nil, nil
}
func (q *censusQueueSpy) ObservePendingCensus(_ context.Context, identity messagequeue.QueueSessionIdentity, _ string) (messagequeue.PendingCensus, error) {
	q.reads++
	q.identity = identity
	return messagequeue.PendingCensus{Version: 1, Identity: identity, Count: 0, Entries: []messagequeue.PendingCensusEntry{}}, nil
}
func (q *censusQueueSpy) RemoveClaimedPending(_ context.Context, identity messagequeue.QueueSessionIdentity, _, entryID, _ string) (*messagequeue.QueueRemovalResult, error) {
	q.removals++
	q.identity = identity
	return &messagequeue.QueueRemovalResult{Removed: []messagequeue.QueuedMessage{{ID: entryID}}}, nil
}

func TestMessageQueueCensusRegisteredDispatchScopeAndPrequeueValidation(t *testing.T) {
	svc, repo := newTestTaskService(t)
	owner := seedOwnedTask(t, svc, repo, "queue-owner", "owner-user")
	foreign := seedOwnedTask(t, svc, repo, "queue-foreign", "foreign-user")
	session, err := repo.GetTaskSession(context.Background(), owner.sessionID)
	require.NoError(t, err)
	q := &censusQueueSpy{}
	h := &Handlers{taskSvc: svc, messageQueue: q, logger: testLogger(t).WithFields()}
	dispatcher := ws.NewDispatcher()
	h.RegisterHandlers(dispatcher)
	principal := mcpscope.Principal{
		WorkspaceID: owner.workspaceID, CallerTaskID: owner.taskID, CallerSessionID: owner.sessionID,
		OriginalWorkspaceID: owner.workspaceID,
		OriginalOwnerID:     owner.userID,
		CallerIncarnationID: session.QueueIncarnationID, OriginalQueueIncarnationID: session.QueueIncarnationID,
		ExecutionID: "execution-1", Surface: mcpprofile.SurfaceKanbanTask,
	}
	ctx := authn.WithIdentity(context.Background(), authn.Identity{UserID: owner.userID, Role: authn.RoleMember})
	ctx = mcpscope.WithPrincipal(ctx, principal)
	call := func(ctx context.Context, action string, payload any) *ws.Message {
		t.Helper()
		response, err := dispatcher.Dispatch(ctx, makeWSMessage(t, action, payload))
		require.NoError(t, err)
		return response
	}
	require.Equal(t, ws.MessageTypeResponse, call(ctx, ws.ActionMCPMessageQueueCensus, map[string]any{}).Type)
	require.Equal(t, 1, q.reads)
	require.Equal(t, owner.taskID, q.identity.TaskID)
	require.Equal(t, owner.sessionID, q.identity.SessionID)
	for _, malformed := range []any{nil, []string{}, map[string]any{"task_id": foreign.taskID}} {
		require.Equal(t, ws.MessageTypeError, call(ctx, ws.ActionMCPMessageQueueCensus, malformed).Type)
	}
	require.Equal(t, 1, q.reads)
	patOnly := authn.WithIdentity(context.Background(), authn.Identity{UserID: owner.userID, Role: authn.RoleMember})
	require.Equal(t, ws.MessageTypeError, call(patOnly, ws.ActionMCPMessageQueueCensus, map[string]any{}).Type)
	foreignOwner := authn.WithIdentity(context.Background(), authn.Identity{UserID: foreign.userID, Role: authn.RoleMember})
	foreignOwner = mcpscope.WithPrincipal(foreignOwner, principal)
	require.Equal(t, ws.MessageTypeError, call(foreignOwner, ws.ActionMCPMessageQueueCensus, map[string]any{}).Type)
	require.Equal(t, 1, q.reads)
	legacy := principal
	legacy.OriginalQueueIncarnationID = ""
	require.Equal(t, ws.MessageTypeError, call(mcpscope.WithPrincipal(ctx, legacy), ws.ActionMCPMessageQueueCensus, map[string]any{}).Type)
	require.Equal(t, 1, q.reads)
	claim := strings.Repeat("a", 64)
	require.Equal(t, ws.MessageTypeError, call(ctx, ws.ActionMCPRemoveClaimedQueueEntry, map[string]any{"entry_id": "a", "claim": claim, "task_id": foreign.taskID}).Type)
	require.Zero(t, q.removals)
	stale := principal
	stale.OriginalQueueIncarnationID = "former-incarnation"
	require.Equal(t, ws.MessageTypeError, call(mcpscope.WithPrincipal(ctx, stale), ws.ActionMCPMessageQueueCensus, map[string]any{}).Type)
	require.Equal(t, 1, q.reads)
	crossWorkspace := principal
	crossWorkspace.WorkspaceID = foreign.workspaceID
	require.Equal(t, ws.MessageTypeError, call(mcpscope.WithPrincipal(ctx, crossWorkspace), ws.ActionMCPMessageQueueCensus, map[string]any{}).Type)
	require.Equal(t, 1, q.reads)
	transferred := principal
	transferred.OriginalWorkspaceID = foreign.workspaceID
	require.Equal(t, ws.MessageTypeError, call(mcpscope.WithPrincipal(ctx, transferred), ws.ActionMCPMessageQueueCensus, map[string]any{}).Type)
	require.Equal(t, 1, q.reads)
	require.Equal(t, ws.MessageTypeResponse, call(ctx, ws.ActionMCPRemoveClaimedQueueEntry, map[string]any{"entry_id": "a", "claim": claim}).Type)
	require.Equal(t, 1, q.removals)
	response := call(ctx, ws.ActionMCPMessageQueueCensus, map[string]any{})
	var result map[string]any
	require.NoError(t, json.Unmarshal(response.Payload, &result))
	require.NotContains(t, result, "content")
}

func TestClaimedRemovalAttachmentCandidatesUseCommittedRetainedRows(t *testing.T) {
	removal := &messagequeue.QueueRemovalResult{
		Removed: []messagequeue.QueuedMessage{{Attachments: []messagequeue.MessageAttachment{
			{AttachmentID: "only-removed"}, {AttachmentID: "still-retained"}, {AttachmentID: "only-removed"},
		}}},
		Retained: []messagequeue.QueuedMessage{{Attachments: []messagequeue.MessageAttachment{
			{AttachmentID: "still-retained"},
		}}},
	}
	require.Equal(t, "only-removed", claimedRemovalAttachmentCandidates(removal)[0].AttachmentID)
	require.Len(t, claimedRemovalAttachmentCandidates(removal), 1)
}
