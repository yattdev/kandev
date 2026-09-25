package handlers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/common/logger"
	mcpscope "github.com/kandev/kandev/internal/mcp/scope"
	"github.com/kandev/kandev/internal/orchestrator/messagequeue"
	"github.com/kandev/kandev/internal/task/models"
	v1 "github.com/kandev/kandev/pkg/api/v1"
	ws "github.com/kandev/kandev/pkg/websocket"
	"github.com/stretchr/testify/require"
)

type recoverySessionRepo struct {
	SessionRepository
	sessions map[string]*models.TaskSession
}

func (r *recoverySessionRepo) GetTaskSession(_ context.Context, id string) (*models.TaskSession, error) {
	return r.sessions[id], nil
}

type recoveryLauncher struct {
	SessionLauncher
	queue *messagequeue.Service
}

func (l *recoveryLauncher) GetMessageQueue() *messagequeue.Service { return l.queue }

func recoveryPrincipal(taskID, sessionID string) context.Context {
	return mcpscope.WithPrincipal(context.Background(), mcpscope.Principal{
		WorkspaceID: "workspace-1", CallerTaskID: taskID, CallerSessionID: sessionID,
	})
}

func TestTerminalQueueRecoveryRequiresSameTaskTerminalPredecessor(t *testing.T) {
	queue := messagequeue.NewServiceMemory(logger.Default())
	queue.SetAutoMergeEnabled(false)
	_, err := queue.QueueMessage(context.Background(), "old", "task-1", "synthetic body", "", messagequeue.QueuedByAgent, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	h := &Handlers{
		sessionRepo: &recoverySessionRepo{sessions: map[string]*models.TaskSession{
			"old":   {ID: "old", TaskID: "task-1", State: models.TaskSessionStateCancelled},
			"other": {ID: "other", TaskID: "task-2", State: models.TaskSessionStateCancelled},
			"live":  {ID: "live", TaskID: "task-1", State: models.TaskSessionStateRunning},
		}},
		sessionLauncher: &recoveryLauncher{queue: queue}, logger: logger.Default(),
	}
	for _, tc := range []struct{ name, task, session, code string }{
		{"other task", "task-1", "other", ws.ErrorCodeForbidden},
		{"spoofed task", "task-2", "old", ws.ErrorCodeForbidden},
		{"current session", "task-1", "current", ws.ErrorCodeForbidden},
		{"live session", "task-1", "live", ws.ErrorCodeConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := h.handleGetTerminalSessionQueue(recoveryPrincipal("task-1", "current"),
				makeWSMessage(t, ws.ActionMCPGetTerminalSessionQueue, map[string]interface{}{"task_id": tc.task, "session_id": tc.session}))
			if err != nil {
				t.Fatal(err)
			}
			assertWSError(t, resp, tc.code)
		})
	}
	resp, err := h.handleGetTerminalSessionQueue(recoveryPrincipal("task-1", "current"),
		makeWSMessage(t, ws.ActionMCPGetTerminalSessionQueue, map[string]interface{}{"task_id": "task-1", "session_id": "old"}))
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Page messagequeue.QueueRecoveryPage `json:"page"`
	}
	if err := resp.ParsePayload(&payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Page.Fragments) != 1 || payload.Page.Fragments[0].Body != "synthetic body" {
		t.Fatalf("unexpected recovery page")
	}
}

func TestQueueCensusBindsCurrentSessionAndHidesBody(t *testing.T) {
	queue := messagequeue.NewServiceMemory(logger.Default())
	_, err := queue.QueueMessage(context.Background(), "current", "task-1", "synthetic secret", "", messagequeue.QueuedByAgent, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	h := &Handlers{sessionLauncher: &recoveryLauncher{queue: queue}, logger: logger.Default()}
	resp, err := h.handleGetMessageQueueCensus(recoveryPrincipal("task-1", "current"),
		makeWSMessage(t, ws.ActionMCPGetMessageQueueCensus, map[string]interface{}{"task_id": "task-1", "session_id": "old"}))
	if err != nil {
		t.Fatal(err)
	}
	assertWSError(t, resp, ws.ErrorCodeForbidden)
	resp, err = h.handleGetMessageQueueCensus(recoveryPrincipal("task-1", "current"),
		makeWSMessage(t, ws.ActionMCPGetMessageQueueCensus, map[string]interface{}{"task_id": "task-1", "session_id": "current"}))
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]interface{}
	if err := resp.ParsePayload(&payload); err != nil {
		t.Fatal(err)
	}
	if payload["before_count"] != float64(1) {
		t.Fatalf("unexpected census count: %v", payload["before_count"])
	}
	if _, ok := payload["body"]; ok {
		t.Fatal("census exposed body")
	}
}

func TestChildQueueRecoveryDirectParentAndStableSnapshot(t *testing.T) {
	ctx := context.Background()
	svc, repo := newTestTaskService(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "workspace-1", Name: "One"}))
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "workspace-2", Name: "Two"}))
	require.NoError(t, repo.CreateWorkflow(ctx, &models.Workflow{ID: "workflow-1", WorkspaceID: "workspace-1", Name: "Board"}))
	require.NoError(t, repo.CreateWorkflow(ctx, &models.Workflow{ID: "workflow-2", WorkspaceID: "workspace-2", Name: "Board"}))
	now := time.Now()
	for _, task := range []*models.Task{
		{ID: "parent", WorkspaceID: "workspace-1", WorkflowID: "workflow-1", Title: "Parent", State: v1.TaskStateTODO, CreatedAt: now, UpdatedAt: now},
		{ID: "child", ParentID: "parent", WorkspaceID: "workspace-1", WorkflowID: "workflow-1", Title: "Child", State: v1.TaskStateTODO, CreatedAt: now, UpdatedAt: now},
		{ID: "top-level", WorkspaceID: "workspace-1", WorkflowID: "workflow-1", Title: "Top", State: v1.TaskStateTODO, CreatedAt: now, UpdatedAt: now},
		{ID: "foreign", ParentID: "parent", WorkspaceID: "workspace-2", WorkflowID: "workflow-2", Title: "Foreign", State: v1.TaskStateTODO, CreatedAt: now, UpdatedAt: now},
	} {
		require.NoError(t, repo.CreateTask(ctx, task))
	}
	for _, session := range []*models.TaskSession{
		{ID: "caller", TaskID: "parent", State: models.TaskSessionStateRunning, StartedAt: now},
		{ID: "child-wfi", TaskID: "child", State: models.TaskSessionStateWaitingForInput, StartedAt: now},
		{ID: "child-done", TaskID: "child", State: models.TaskSessionStateCancelled, StartedAt: now},
		{ID: "top-session", TaskID: "top-level", State: models.TaskSessionStateCancelled, StartedAt: now},
		{ID: "foreign-session", TaskID: "foreign", State: models.TaskSessionStateCancelled, StartedAt: now},
	} {
		require.NoError(t, repo.CreateTaskSession(ctx, session))
	}
	queue := messagequeue.NewServiceMemory(logger.Default())
	queue.SetAutoMergeEnabled(false)
	before, err := queue.QueueMessage(ctx, "child-wfi", "child", strings.Repeat("x", 2048), "", messagequeue.QueuedByAgent, false, nil)
	require.NoError(t, err)
	_, err = queue.QueueMessage(ctx, "top-session", "top-level", "top secret", "", messagequeue.QueuedByAgent, false, nil)
	require.NoError(t, err)
	h := &Handlers{taskSvc: svc, sessionRepo: repo, sessionLauncher: &recoveryLauncher{queue: queue}, logger: logger.Default()}
	principal := recoveryPrincipal("parent", "caller")
	for _, target := range []string{"parent", "top-level", "foreign"} {
		resp, err := h.handleGetChildQueueInventory(principal, makeWSMessage(t, ws.ActionMCPGetChildQueueInventory, map[string]interface{}{"task_id": target}))
		require.NoError(t, err)
		assertWSError(t, resp, ws.ErrorCodeForbidden)
	}
	resp, err := h.handleGetChildQueueInventory(principal, makeWSMessage(t, ws.ActionMCPGetChildQueueInventory, map[string]interface{}{"task_id": "child"}))
	require.NoError(t, err)
	var inventory struct {
		Sessions []struct {
			SessionID string                             `json:"session_id"`
			Manifest  messagequeue.QueueRecoveryManifest `json:"manifest"`
		} `json:"sessions"`
	}
	require.NoError(t, resp.ParsePayload(&inventory))
	require.Len(t, inventory.Sessions, 2)
	var snapshot string
	for _, session := range inventory.Sessions {
		if session.SessionID == "child-wfi" {
			require.Equal(t, 1, session.Manifest.TotalEntries)
			require.Equal(t, before.ID, session.Manifest.Entries[0].ID)
			encoded, err := json.Marshal(session.Manifest.Entries[0])
			require.NoError(t, err)
			require.NotContains(t, string(encoded), "body")
			snapshot = session.Manifest.SnapshotSHA256
		}
	}
	require.NotEmpty(t, snapshot)
	for _, target := range []struct{ task, session string }{{"child", "top-session"}, {"top-level", "top-session"}, {"foreign", "foreign-session"}} {
		resp, err := h.handleGetChildSessionQueue(principal, makeWSMessage(t, ws.ActionMCPGetChildSessionQueue, map[string]interface{}{"task_id": target.task, "session_id": target.session}))
		require.NoError(t, err)
		assertWSError(t, resp, ws.ErrorCodeForbidden)
	}
	resp, err = h.handleGetChildSessionQueue(principal, makeWSMessage(t, ws.ActionMCPGetChildSessionQueue, map[string]interface{}{"task_id": "child", "session_id": "child-wfi", "max_body_bytes": 1024}))
	require.NoError(t, err)
	var first struct {
		Page messagequeue.QueueRecoveryPage `json:"page"`
	}
	require.NoError(t, resp.ParsePayload(&first))
	require.Equal(t, snapshot, first.Page.SnapshotSHA256)
	require.NotEmpty(t, first.Page.NextCursor)
	require.Equal(t, 1024, first.Page.PageBodyBytes)
	second, err := queue.RecoverySnapshotManifest(ctx, "child-wfi")
	require.NoError(t, err)
	require.Equal(t, snapshot, second.SnapshotSHA256)
	_, err = queue.QueueMessage(ctx, "child-wfi", "child", "later", "", messagequeue.QueuedByAgent, false, nil)
	require.NoError(t, err)
	resp, err = h.handleGetChildSessionQueue(principal, makeWSMessage(t, ws.ActionMCPGetChildSessionQueue, map[string]interface{}{"task_id": "child", "session_id": "child-wfi", "cursor": first.Page.NextCursor}))
	require.NoError(t, err)
	assertWSError(t, resp, ws.ErrorCodeConflict)
	// A rejected cursor leaves the original message and unrelated queues intact.
	remaining, err := queue.RecoverySnapshotManifest(ctx, "child-wfi")
	require.NoError(t, err)
	require.Equal(t, before.ID, remaining.Entries[0].ID)
	unrelated, err := queue.RecoverySnapshotManifest(ctx, "top-session")
	require.NoError(t, err)
	require.Equal(t, 1, unrelated.TotalEntries)
	encoded, err := json.Marshal(inventory)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "top secret")
}
