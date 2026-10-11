package handlers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/mcp/profile"
	"github.com/kandev/kandev/internal/mcp/scope"
	"github.com/kandev/kandev/internal/task/models"
	sqliterepo "github.com/kandev/kandev/internal/task/repository/sqlite"
	v1 "github.com/kandev/kandev/pkg/api/v1"
	ws "github.com/kandev/kandev/pkg/websocket"
	"github.com/stretchr/testify/require"
)

func TestScopedPRURLTrustedParentDispatch(t *testing.T) {
	h, _, ctx, msg, recorder := scopedPRHandlerFixture(t)
	response, err := h.handleManageTaskChangeRequest(ctx, msg)
	require.NoError(t, err)
	require.Equal(t, ws.MessageTypeResponse, response.Type, string(response.Payload))
	encoded, err := json.Marshal(recorder.request)
	require.NoError(t, err)
	require.Contains(t, string(encoded), "https://github.com/upstream/project/pull/42")
}

func scopedPRHandlerFixture(t *testing.T) (*Handlers, *sqliterepo.Repository, context.Context, *ws.Message, *richTaskChangeRequestRecorder) {
	t.Helper()
	svc, repo := newTestTaskService(t)
	ctx := context.Background()
	now := time.Now().UTC()
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "ws", Name: "ws", CreatedAt: now, UpdatedAt: now}))
	for _, task := range []*models.Task{{ID: "parent", WorkspaceID: "ws", Title: "Parent", State: v1.TaskStateInProgress, CreatedAt: now, UpdatedAt: now}, {ID: "child", WorkspaceID: "ws", ParentID: "parent", Title: "Child", State: v1.TaskStateTODO, CreatedAt: now, UpdatedAt: now}} {
		require.NoError(t, repo.CreateTask(ctx, task))
	}
	require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{ID: "session", TaskID: "parent", State: models.TaskSessionStateRunning, StartedAt: now, UpdatedAt: now}))
	recorder := &richTaskChangeRequestRecorder{taskChangeRequestRecorder: &taskChangeRequestRecorder{}, result: TaskChangeLinkMutationResult{TaskID: "child", StateKnown: true}}
	h := NewHandlers(svc, nil, nil, nil, nil, repo, repo, nil, nil, nil, nil, nil, testLogger(t))
	h.SetTaskChangeLinkService(recorder)
	ctx = scope.WithPrincipal(ctx, scope.Principal{CallerTaskID: "parent", CallerSessionID: "session", WorkspaceID: "ws", Surface: profile.SurfaceKanbanTask})
	msg := makeWSMessage(t, ws.ActionMCPManageTaskChangeRequest, map[string]interface{}{"operation": "link", "task_id": "child", "caller_task_id": "parent", "provider": "github", "repository_id": "fork", "number": 42, "pr_url": "https://github.com/upstream/project/pull/42", "expected_head_sha": strings.Repeat("a", 40)})
	return h, repo, ctx, msg, recorder
}

func TestScopedPRURLHandlerDeniesUnsupportedAuthority(t *testing.T) {
	for _, mode := range []string{"raw unbound", "self", "missing session", "foreign session", "terminal session", "nonparent", "foreign workspace", "automation surface", "config surface", "injected caller", "short SHA", "revoked configuration", "revoked managed policy"} {
		t.Run(mode, func(t *testing.T) {
			h, repo, ctx, msg, recorder := scopedPRHandlerFixture(t)
			var args map[string]interface{}
			require.NoError(t, json.Unmarshal(msg.Payload, &args))
			p, _ := scope.PrincipalFromContext(ctx)
			switch mode {
			case "raw unbound":
				ctx = context.Background()
			case "self":
				args["task_id"] = "parent"
			case "missing session":
				p.CallerSessionID = "missing"
			case "foreign session":
				p.CallerTaskID = "child"
				args["caller_task_id"] = "child"
			case "terminal session":
				session, err := repo.GetTaskSession(ctx, "session")
				require.NoError(t, err)
				session.State = models.TaskSessionStateCompleted
				require.NoError(t, repo.UpdateTaskSession(ctx, session))
			case "nonparent":
				now := time.Now().UTC()
				require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: "unrelated", WorkspaceID: "ws", ParentID: "other", Title: "Unrelated", State: v1.TaskStateTODO, CreatedAt: now, UpdatedAt: now}))
				args["task_id"] = "unrelated"
			case "foreign workspace":
				p.WorkspaceID = "foreign"
			case "automation surface":
				p.Surface = profile.SurfaceAutomation
			case "config surface":
				p.Surface = profile.SurfaceConfiguration
			case "injected caller":
				args["caller_task_id"] = "spoof"
			case "short SHA":
				args["expected_head_sha"] = "abcdefa"
			case "revoked configuration":
				require.NoError(t, repo.SetSessionMetadataKey(ctx, "session", "config_mode", true))
				session, err := repo.GetTaskSession(ctx, "session")
				require.NoError(t, err)
				require.Equal(t, true, session.Metadata["config_mode"])
			case "revoked managed policy":
				task, err := repo.GetTask(ctx, "parent")
				require.NoError(t, err)
				task.Metadata = map[string]interface{}{models.MetaKeyManagedRetained: true}
				require.NoError(t, repo.UpdateTask(ctx, task))
			}
			if mode != "raw unbound" {
				ctx = scope.WithPrincipal(ctx, p)
			}
			msg.Payload, _ = json.Marshal(args)
			response, err := h.handleManageTaskChangeRequest(ctx, msg)
			require.NoError(t, err)
			require.Equal(t, ws.MessageTypeError, response.Type)
			require.Empty(t, recorder.operation)
		})
	}
}
