package handlers

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/agentctl/types/streams"
	"github.com/kandev/kandev/internal/auth/authn"
	mcpprofile "github.com/kandev/kandev/internal/mcp/profile"
	mcpscope "github.com/kandev/kandev/internal/mcp/scope"
	"github.com/kandev/kandev/internal/orchestrator"
	"github.com/kandev/kandev/internal/orchestrator/messagequeue"
	"github.com/kandev/kandev/internal/task/models"
	ws "github.com/kandev/kandev/pkg/websocket"
	"github.com/stretchr/testify/require"
)

type censusLauncher struct {
	SessionLauncher
	called int
}

func (l *censusLauncher) GetActiveQueueCensus(context.Context) (*messagequeue.ActiveQueueCensus, error) {
	l.called++
	return &messagequeue.ActiveQueueCensus{Version: 1, Complete: true, Entries: []messagequeue.ActiveQueueCensusEntry{}}, nil
}

// @covers AC-TASKS-ACTIVE-QUEUE-CENSUS-001.2 AC-TASKS-ACTIVE-QUEUE-CENSUS-001.3
func TestActiveQueueCensusRawActionDeniedWithoutPrincipal(t *testing.T) {
	h := &Handlers{}
	handler, ok := interface{}(h).(interface {
		handleGetMessageQueueCensus(context.Context, *ws.Message) (*ws.Message, error)
	})
	require.True(t, ok, "raw census action must have an execution-time guard")
	request, err := ws.NewRequest("request", ws.ActionMCPGetMessageQueueCensus, map[string]interface{}{})
	require.NoError(t, err)
	response, err := handler.handleGetMessageQueueCensus(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, ws.MessageTypeError, response.Type)
}

// @covers AC-TASKS-ACTIVE-QUEUE-CENSUS-001.1 AC-TASKS-ACTIVE-QUEUE-CENSUS-001.3
func TestActiveQueueCensusRawActionRequiresExactContextAndEmptyObject(t *testing.T) {
	launcher := &censusLauncher{}
	h := &Handlers{sessionLauncher: launcher}
	base := streams.WithMCPExecutionContext(context.Background(), streams.MCPExecutionContext{
		TaskID: "task", SessionID: "session", ExecutionID: "execution",
	})
	ctx := mcpscope.WithPrincipal(base, mcpscope.Principal{
		CallerTaskID: "task", CallerSessionID: "session", WorkspaceID: "workspace",
		Surface: mcpprofile.SurfaceKanbanTask,
	})
	valid, err := ws.NewRequest("valid", ws.ActionMCPGetMessageQueueCensus, map[string]interface{}{})
	require.NoError(t, err)
	response, err := h.handleGetMessageQueueCensus(ctx, valid)
	require.NoError(t, err)
	require.Equal(t, ws.MessageTypeResponse, response.Type)
	require.JSONEq(t, `{"version":1,"task_id":"","session_id":"","session_incarnation_id":"","snapshot_revision_sha256":"","complete":true,"count":0,"capacity":0,"entries":[]}`, string(response.Payload))
	require.Equal(t, 1, launcher.called)
	dispatcher := ws.NewDispatcher()
	h.logger = testLogger(t).WithFields()
	h.RegisterHandlers(dispatcher)
	dispatched, err := dispatcher.Dispatch(ctx, valid)
	require.NoError(t, err)
	require.Equal(t, ws.MessageTypeResponse, dispatched.Type)
	require.Equal(t, 2, launcher.called)

	for _, raw := range []string{`null`, `[]`, `{"task_id":"task"}`, `{"x":1,"x":2}`, `{} {}`} {
		invalid := *valid
		invalid.Payload = json.RawMessage(raw)
		response, err := h.handleGetMessageQueueCensus(ctx, &invalid)
		require.NoError(t, err)
		require.Equal(t, ws.MessageTypeError, response.Type, raw)
	}
	require.Equal(t, 2, launcher.called)

	wrong := mcpscope.WithPrincipal(base, mcpscope.Principal{
		CallerTaskID: "other", CallerSessionID: "session", WorkspaceID: "workspace",
		Surface: mcpprofile.SurfaceOfficeTask,
	})
	response, err = h.handleGetMessageQueueCensus(wrong, valid)
	require.NoError(t, err)
	require.Equal(t, ws.MessageTypeError, response.Type)
	require.Equal(t, 2, launcher.called)
}

// @covers AC-TASKS-ACTIVE-QUEUE-CENSUS-001.1 AC-TASKS-ACTIVE-QUEUE-CENSUS-001.2 AC-TASKS-ACTIVE-QUEUE-CENSUS-001.6
func TestActiveQueueCensusTrustedDispatcherToMemoryProvider(t *testing.T) {
	ctx := context.Background()
	taskSvc, repo := newTestTaskService(t)
	owner := seedOwnedTask(t, taskSvc, repo, "census", "user-a")
	session, err := repo.GetTaskSession(ctx, owner.sessionID)
	require.NoError(t, err)
	queueRepo := messagequeue.NewMemoryRepositoryWithAuthority(func(ctx context.Context, taskID, sessionID string) (messagequeue.QueueSessionIdentity, error) {
		current, err := repo.GetTaskSession(ctx, sessionID)
		if err != nil {
			return messagequeue.QueueSessionIdentity{}, err
		}
		return messagequeue.QueueSessionIdentity{TaskID: current.TaskID, SessionID: current.ID, SessionIncarnationID: current.QueueIncarnationID}, nil
	})
	identity := messagequeue.QueueSessionIdentity{TaskID: owner.taskID, SessionID: owner.sessionID, SessionIncarnationID: session.QueueIncarnationID}
	_, err = queueRepo.ResolveSessionIdentity(ctx, owner.taskID, owner.sessionID)
	require.NoError(t, err)
	require.NoError(t, queueRepo.InsertForSession(ctx, identity, &messagequeue.QueuedMessage{
		ID: "pending", TaskID: owner.taskID, SessionID: owner.sessionID, Content: "private body",
	}, 10))
	require.NoError(t, repo.UpsertExecutorRunning(ctx, &models.ExecutorRunning{
		ID: "running", TaskID: owner.taskID, SessionID: owner.sessionID, AgentExecutionID: "current",
		Status: models.ExecutorRunningStatusRunning, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}))
	queue := messagequeue.NewService(queueRepo, 10, testLogger(t))
	launcher := orchestrator.NewService(orchestrator.DefaultServiceConfig(), nil, nil, nil, repo, nil, nil, queue, testLogger(t))
	h := &Handlers{sessionLauncher: launcher, logger: testLogger(t).WithFields()}
	dispatcher := ws.NewDispatcher()
	h.RegisterHandlers(dispatcher)
	ctx = streams.WithMCPExecutionContext(ctx, streams.MCPExecutionContext{
		TaskID: owner.taskID, SessionID: owner.sessionID, ExecutionID: "current",
	})
	ctx = authn.WithIdentity(ctx, authn.Identity{UserID: owner.userID, Role: authn.RoleMember})
	ctx = mcpscope.WithPrincipal(ctx, mcpscope.Principal{
		CallerTaskID: owner.taskID, CallerSessionID: owner.sessionID, WorkspaceID: owner.workspaceID,
		Surface: mcpprofile.SurfaceKanbanTask,
	})
	request, err := ws.NewRequest("census", ws.ActionMCPGetMessageQueueCensus, map[string]interface{}{})
	require.NoError(t, err)
	response, err := dispatcher.Dispatch(ctx, request)
	require.NoError(t, err)
	require.Equal(t, ws.MessageTypeResponse, response.Type)
	var result messagequeue.ActiveQueueCensus
	require.NoError(t, json.Unmarshal(response.Payload, &result))
	require.Equal(t, 1, result.Count)
	require.Equal(t, "pending", result.Entries[0].ID)
	require.NotContains(t, string(response.Payload), "private body")
}
