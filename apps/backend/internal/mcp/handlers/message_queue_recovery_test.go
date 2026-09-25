package handlers

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/common/logger"
	mcpscope "github.com/kandev/kandev/internal/mcp/scope"
	"github.com/kandev/kandev/internal/orchestrator/messagequeue"
	"github.com/kandev/kandev/internal/task/models"
	ws "github.com/kandev/kandev/pkg/websocket"
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
