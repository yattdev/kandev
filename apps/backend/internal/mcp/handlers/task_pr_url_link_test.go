package handlers

import (
	"context"
	"testing"
	"time"

	mcpprofile "github.com/kandev/kandev/internal/mcp/profile"
	mcpscope "github.com/kandev/kandev/internal/mcp/scope"
	"github.com/kandev/kandev/internal/task/models"
	v1 "github.com/kandev/kandev/pkg/api/v1"
	ws "github.com/kandev/kandev/pkg/websocket"
	"github.com/stretchr/testify/require"
)

type recordingURLLinkService struct {
	taskChangeRequestRecorder
	called bool
}

func (r *recordingURLLinkService) LinkTaskPRByURL(_ context.Context, taskID, repositoryID, url string) (TaskPRURLLinkResult, error) {
	r.called = true
	return TaskPRURLLinkResult{TaskID: taskID, RepositoryID: repositoryID, PRURL: url, StateKnown: true}, nil
}

func TestLinkTaskPRByURLRequiresTrustedDirectParent(t *testing.T) {
	svc, repo := newTestTaskService(t)
	now := time.Now().UTC()
	ctx := context.Background()
	for _, workspace := range []string{"workspace-1", "workspace-2"} {
		require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: workspace, Name: workspace, CreatedAt: now, UpdatedAt: now}))
	}
	for _, task := range []*models.Task{
		{ID: "parent", WorkspaceID: "workspace-1", Title: "Parent", State: v1.TaskStateTODO, CreatedAt: now, UpdatedAt: now},
		{ID: "child", ParentID: "parent", WorkspaceID: "workspace-1", Title: "Child", State: v1.TaskStateTODO, CreatedAt: now, UpdatedAt: now},
		{ID: "other", ParentID: "someone-else", WorkspaceID: "workspace-1", Title: "Other", State: v1.TaskStateTODO, CreatedAt: now, UpdatedAt: now},
		{ID: "foreign", ParentID: "parent", WorkspaceID: "workspace-2", Title: "Foreign", State: v1.TaskStateTODO, CreatedAt: now, UpdatedAt: now},
	} {
		require.NoError(t, repo.CreateTask(ctx, task))
	}
	recorder := &recordingURLLinkService{}
	h := NewHandlers(svc, nil, nil, nil, nil, repo, repo, nil, nil, nil, nil, nil, testLogger(t))
	h.SetTaskChangeLinkService(recorder)
	trusted := mcpscope.WithPrincipal(ctx, mcpscope.Principal{WorkspaceID: "workspace-1", CallerTaskID: "parent", CallerSessionID: "session", Surface: mcpprofile.SurfaceKanbanTask})
	for _, tc := range []struct {
		target, caller string
		permitted      bool
	}{
		{"child", "parent", true}, {"other", "parent", false}, {"foreign", "parent", false}, {"child", "spoofed", false},
	} {
		recorder.called = false
		msg := makeWSMessage(t, ws.ActionMCPLinkTaskPRByURL, map[string]interface{}{
			"task_id": tc.target, "caller_task_id": tc.caller, "repository_id": "repo",
			"pr_url": "https://github.com/kdlbs/kandev/pull/3166",
		})
		response, err := h.handleLinkTaskPRByURL(trusted, msg)
		require.NoError(t, err)
		require.Equal(t, tc.permitted, response.Type == ws.MessageTypeResponse)
		require.Equal(t, tc.permitted, recorder.called)
	}
}
