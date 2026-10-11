package backendapp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/kandev/kandev/internal/auth/authn"
	"github.com/kandev/kandev/internal/github"
	mcp "github.com/kandev/kandev/internal/mcp/handlers"
	"github.com/kandev/kandev/internal/mcp/profile"
	"github.com/kandev/kandev/internal/mcp/scope"
	"github.com/kandev/kandev/internal/task/models"
	v1 "github.com/kandev/kandev/pkg/api/v1"
	ws "github.com/kandev/kandev/pkg/websocket"
	"github.com/stretchr/testify/require"
)

func TestScopedPRURLFullManagementPath(t *testing.T) {
	f := newScopedBackendFixture(t)
	dispatcher, ctx, msg, store := f.dispatcher, f.ctx, f.msg, f.store
	for range 2 {
		response, err := dispatcher.Dispatch(ctx, msg)
		require.NoError(t, err)
		require.Equal(t, ws.MessageTypeResponse, response.Type, string(response.Payload))
		var result mcp.TaskChangeLinkMutationResult
		require.NoError(t, json.Unmarshal(response.Payload, &result))
		require.True(t, result.StateKnown)
		require.Equal(t, "https://github.com/upstream/project/pull/42", result.Resolved.URL)
		require.Equal(t, "fork", result.Resolved.RepositoryID)
	}
	rows, err := store.ListTaskPRsByTask(ctx, "child")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "merger", *rows[0].MergedByLogin)
	require.Equal(t, 3, *rows[0].ChangedFiles)
	parentRows, err := store.ListTaskPRsByTask(ctx, "parent")
	require.NoError(t, err)
	require.Empty(t, parentRows)
}

type scopedBackendFixture struct {
	dispatcher  *ws.Dispatcher
	ctx         context.Context
	msg         *ws.Message
	store       *github.Store
	svc         *github.Service
	client      *github.MockClient
	handler     *mcp.Handlers
	coordinator taskChangeLinkCoordinator
	database    *sqlx.DB
}

func newScopedBackendFixture(t *testing.T) *scopedBackendFixture {
	t.Helper()
	tasks, repo, database := newTaskChangeCoordinatorHarnessDB(t)
	seedTaskChangeCoordinatorTask(t, repo, "ws", "child", "fork", "https://github.com", "fork", "project")
	now := time.Now().UTC()
	ctx := context.Background()
	require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: "parent", WorkspaceID: "ws", Title: "Parent", State: v1.TaskStateInProgress, CreatedAt: now, UpdatedAt: now}))
	require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{ID: "session", TaskID: "parent", State: models.TaskSessionStateRunning, StartedAt: now, UpdatedAt: now}))
	_, err := database.Exec(`UPDATE tasks SET parent_id='parent' WHERE id='child'`)
	require.NoError(t, err)
	store, err := github.NewStore(database, database)
	require.NoError(t, err)
	client := github.NewMockClient()
	client.AddRepos("upstream", []github.GitHubRepo{{Owner: "upstream", Name: "project", FullName: "upstream/project"}})
	client.SetPRDetail("upstream", "project", 42, &github.PR{Number: 42, State: "merged", RepoOwner: "upstream", RepoName: "project", BaseRepoOwner: "upstream", BaseRepoName: "project", HeadRepoOwner: "fork", HeadRepoName: "project", HeadBranch: "feature", BaseBranch: "main", HeadSHA: strings.Repeat("a", 40), HTMLURL: "https://github.com/upstream/project/pull/42", CreatedAt: now, MergedAt: &now, MergedByLogin: "merger", ChangedFilesObserved: true, ChangedFiles: 3}, nil)
	log := newTestLogger()
	svc := github.NewService(client, github.AuthMethodPAT, nil, store, nil, log)
	github.NewMockController(client, store, nil, svc, log)
	require.NoError(t, store.UpsertWorkspaceConnection(ctx, &github.WorkspaceConnection{WorkspaceID: "ws", Source: github.ConnectionSourcePAT, GitHubHost: "github.com", Login: "test-user", Status: github.ConnectionStatusActive, CredentialGeneration: 1}))
	c := taskChangeLinkCoordinator{tasks: tasks, github: svc, singleUserIdentity: func() (authn.Identity, bool) { return authn.Identity{UserID: "user"}, true }}
	h := mcp.NewHandlers(tasks, nil, nil, nil, nil, repo, repo, nil, nil, nil, nil, nil, log)
	h.SetTaskChangeLinkService(c)
	dispatcher := ws.NewDispatcher()
	h.RegisterHandlers(dispatcher)
	ctx = scope.WithPrincipal(ctx, scope.Principal{CallerTaskID: "parent", CallerSessionID: "session", WorkspaceID: "ws", Surface: profile.SurfaceKanbanTask})
	payload, err := json.Marshal(map[string]interface{}{"operation": "link", "task_id": "child", "caller_task_id": "parent", "provider": "github", "repository_id": "fork", "number": 42, "pr_url": "https://github.com/upstream/project/pull/42", "expected_head_sha": strings.Repeat("a", 40)})
	require.NoError(t, err)
	msg := &ws.Message{ID: "url-link", Type: ws.MessageTypeRequest, Action: ws.ActionMCPManageTaskChangeRequest, Payload: payload}
	return &scopedBackendFixture{dispatcher: dispatcher, ctx: ctx, msg: msg, store: store, svc: svc, client: client, handler: h, coordinator: c, database: database}
}
