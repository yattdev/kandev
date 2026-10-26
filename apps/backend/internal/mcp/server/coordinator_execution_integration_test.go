package mcp

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"

	"github.com/kandev/kandev/internal/agent/runtime/lifecycle"
	"github.com/kandev/kandev/internal/auth/authn"
	"github.com/kandev/kandev/internal/coordinator"
	"github.com/kandev/kandev/internal/db"
	"github.com/kandev/kandev/internal/events/bus"
	backendmcp "github.com/kandev/kandev/internal/mcp/handlers"
	mcpprofile "github.com/kandev/kandev/internal/mcp/profile"
	"github.com/kandev/kandev/internal/mcp/scope"
	officesqlite "github.com/kandev/kandev/internal/office/repository/sqlite"
	"github.com/kandev/kandev/internal/task/models"
	tasksqlite "github.com/kandev/kandev/internal/task/repository/sqlite"
	"github.com/kandev/kandev/internal/task/service"
	v1 "github.com/kandev/kandev/pkg/api/v1"
	ws "github.com/kandev/kandev/pkg/websocket"
)

type coordinatorFixtureIdentity struct{}

func (coordinatorFixtureIdentity) IdentityForUser(_ context.Context, userID string) (authn.Identity, bool) {
	if userID != "owner" {
		return authn.Identity{}, false
	}
	return authn.Identity{UserID: userID, Role: authn.RoleMember}, true
}

// A stored conversation and execution must pass through the production lifecycle
// wrapper before the typed dependency tool reaches the audited SQLite writer.
func TestCoordinatorDependencyStoredExecutionDispatch(t *testing.T) {
	log := newTestLogger(t)
	conn, err := db.OpenSQLite(filepath.Join(t.TempDir(), "coordinator-dependencies.db"))
	require.NoError(t, err)
	database := sqlx.NewDb(conn, "sqlite3")
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	repo, err := tasksqlite.NewWithDB(database, database, log)
	require.NoError(t, err)
	officeRepo, err := officesqlite.NewWithDB(database, database, nil)
	require.NoError(t, err)
	eventBus := bus.NewMemoryEventBus(log)
	t.Cleanup(eventBus.Close)
	tasks := service.NewService(service.Repos{
		Workspaces: repo, Tasks: repo, TaskRepos: repo, WorkspaceFolders: repo,
		Workflows: repo, Messages: repo, Turns: repo, Sessions: repo,
		GitSnapshots: repo, RepoEntities: repo, Executors: repo,
		Environments: repo, TaskEnvironments: repo, Reviews: repo,
	}, eventBus, log, service.RepositoryDiscoveryConfig{})
	tasks.SetBlockerRepository(officeRepo)
	ctx := t.Context()
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "ws", Name: "Fixture", OwnerID: "owner"}))
	require.NoError(t, repo.CreateWorkflow(ctx, &models.Workflow{ID: "watched", WorkspaceID: "ws", Name: "Watched"}))
	require.NoError(t, repo.CreateWorkflow(ctx, &models.Workflow{ID: "outside", WorkspaceID: "ws", Name: "Outside"}))
	store, err := coordinator.NewStore(database, database)
	require.NoError(t, err)
	coord := &coordinator.Coordinator{WorkspaceID: "ws", Name: "Fixture coordinator", AgentProfileID: "agent", ExecutorProfileID: "executor"}
	require.NoError(t, store.CreateCoordinator(ctx, coord))
	policy := mcpprofile.CoordinatorToolPolicy{
		Version: 1, CoordinatorID: coord.ID, WorkspaceID: "ws", ConversationTaskID: "conversation",
		ToolNames: []string{"add_task_dependency_kandev", "remove_task_dependency_kandev"},
	}
	encoded, err := mcpprofile.MarshalCoordinatorToolPolicy(policy)
	require.NoError(t, err)
	now := time.Now().UTC()
	for _, task := range []*models.Task{
		{ID: "conversation", WorkspaceID: "ws", WorkflowID: "watched", Title: "Coordinator", Origin: models.TaskOriginCoordinator, State: v1.TaskStateInProgress, Metadata: map[string]interface{}{mcpprofile.CoordinatorToolPolicyMetadataKey: encoded}, CreatedAt: now, UpdatedAt: now},
		{ID: "dependent", WorkspaceID: "ws", WorkflowID: "watched", Title: "Dependent", State: v1.TaskStateInProgress, CreatedAt: now, UpdatedAt: now},
		{ID: "predecessor", WorkspaceID: "ws", WorkflowID: "watched", Title: "Predecessor", State: v1.TaskStateInProgress, CreatedAt: now, UpdatedAt: now},
		{ID: "unwatched", WorkspaceID: "ws", WorkflowID: "outside", Title: "Outside", State: v1.TaskStateInProgress, CreatedAt: now, UpdatedAt: now},
		{ID: "project-out", WorkspaceID: "ws", WorkflowID: "watched", Title: "Other project", State: v1.TaskStateInProgress, CreatedAt: now, UpdatedAt: now},
		{ID: "ordinary", WorkspaceID: "ws", WorkflowID: "watched", Title: "Ordinary task", State: v1.TaskStateInProgress, CreatedAt: now, UpdatedAt: now},
	} {
		require.NoError(t, repo.CreateTask(ctx, task))
	}
	for _, repository := range []*models.Repository{
		{ID: "repo-in", WorkspaceID: "ws", Name: "In scope", SourceType: "local", LocalPath: filepath.Join(t.TempDir(), "in")},
		{ID: "repo-out", WorkspaceID: "ws", Name: "Out of scope", SourceType: "local", LocalPath: filepath.Join(t.TempDir(), "out")},
	} {
		require.NoError(t, repo.CreateRepository(ctx, repository))
	}
	for _, linked := range []struct{ taskID, repositoryID string }{
		{"dependent", "repo-in"}, {"predecessor", "repo-in"}, {"project-out", "repo-out"},
	} {
		require.NoError(t, repo.CreateTaskRepository(ctx, &models.TaskRepository{
			TaskID: linked.taskID, RepositoryID: linked.repositoryID, BaseBranch: "main",
		}))
	}
	_, err = database.ExecContext(ctx, `INSERT INTO task_sessions(id, task_id, state, started_at, updated_at) VALUES('session', 'conversation', 'RUNNING', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	require.NoError(t, err)
	_, err = database.ExecContext(ctx, `INSERT INTO executors_running(id, session_id, task_id, executor_id, status, agent_execution_id, created_at, updated_at) VALUES('runner', 'session', 'conversation', 'local', 'running', 'execution', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	require.NoError(t, err)
	_, err = database.ExecContext(ctx, `INSERT INTO task_sessions(id, task_id, state, started_at, updated_at) VALUES('ordinary-session', 'ordinary', 'RUNNING', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	require.NoError(t, err)
	_, err = database.ExecContext(ctx, `INSERT INTO executors_running(id, session_id, task_id, executor_id, status, agent_execution_id, created_at, updated_at) VALUES('ordinary-runner', 'ordinary-session', 'ordinary', 'local', 'running', 'ordinary-execution', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	require.NoError(t, err)
	coordinatorSvc := coordinator.NewService(store, coordinator.NewValidator(nil, nil), tasks, log,
		coordinator.WithPhase2(true), coordinator.WithPhase31(true))
	coordinatorSvc.SetProjectReader(tasks)
	_, err = coordinatorSvc.SaveSettings(ctx, "ws", coord.ID, []byte(`{"watches":{"scope":"selected","workflow_ids":["watched"]},"projects":{"scope":"selected","entries":[{"kind":"repository","id":"repo-in"}]}}`))
	require.NoError(t, err)
	_, err = database.ExecContext(ctx, `UPDATE coordinators SET conversation_task_id = 'conversation' WHERE id = ?`, coord.ID)
	require.NoError(t, err)
	handlers := backendmcp.NewHandlers(tasks, nil, nil, nil, nil, repo, repo, eventBus, nil, nil, nil, nil, log)
	handlers.SetCoordinatorService(coordinatorSvc)
	dispatcher := ws.NewDispatcher()
	handlers.RegisterHandlers(dispatcher)
	resolver := scope.NewResolver(repo, coordinatorFixtureIdentity{}, func() bool { return true }, log)
	resolver.SetCoordinatorLookup(coordinatorSvc)
	manager := lifecycle.NewManager(nil, eventBus, nil, nil, nil, nil, lifecycle.ExecutorFallbackWarn, t.TempDir(), log)
	manager.SetMCPHandler(dispatcher)
	manager.SetMCPIdentityScoper(resolver.Scope)
	manager.SetMCPPrincipalScoper(resolver.ScopePrincipal)
	execution := &lifecycle.AgentExecution{ID: "execution", TaskID: "conversation", SessionID: "session"}
	stored, err := repo.GetTask(ctx, "conversation")
	require.NoError(t, err)
	execution.SetMetadataForTesting(stored.Metadata)
	bound, err := mcpprofile.ParseCoordinatorToolPolicyMetadata(stored.Metadata[mcpprofile.CoordinatorToolPolicyMetadataKey])
	require.NoError(t, err)
	profile := mcpprofile.NewCoordinator()
	profile.CoordinatorToolPolicy = bound
	server := NewWithProfile(NewDispatcherBackendClient(manager.MCPHandlerFor(execution), log), "session", "conversation", 10005, log, "", false, profile)
	t.Cleanup(func() { _ = server.Close(context.Background()) })

	args := map[string]interface{}{"task_id": "dependent", "depends_on_task_id": "predecessor"}
	result := callTool(t, server, "add_task_dependency_kandev", args)
	require.False(t, result.IsError, firstText(t, result))
	require.Contains(t, firstText(t, result), "predecessor")
	var edges, audits, revision int
	require.NoError(t, database.Get(&edges, `SELECT COUNT(*) FROM task_blockers WHERE task_id = 'dependent' AND blocker_task_id = 'predecessor'`))
	require.Equal(t, 1, edges)
	require.NoError(t, database.Get(&audits, `SELECT COUNT(*) FROM task_dependency_mutation_audit WHERE outcome = 'succeeded'`))
	require.Equal(t, 1, audits)
	require.NoError(t, database.Get(&revision, `SELECT dependency_revision FROM tasks WHERE id = 'dependent'`))
	require.Equal(t, 1, revision)
	result = callTool(t, server, "add_task_dependency_kandev", map[string]interface{}{
		"task_id": "dependent", "depends_on_task_id": "project-out",
	})
	require.True(t, result.IsError)
	require.NoError(t, database.Get(&edges, `SELECT COUNT(*) FROM task_blockers`))
	require.Equal(t, 1, edges)
	result = callTool(t, server, "add_task_dependency_kandev", map[string]interface{}{
		"task_id": "predecessor", "depends_on_task_id": "dependent",
	})
	require.True(t, result.IsError, "a reverse edge would create a cycle")
	require.NoError(t, database.Get(&edges, `SELECT COUNT(*) FROM task_blockers`))
	require.Equal(t, 1, edges)
	result = callTool(t, server, "add_task_dependency_kandev", map[string]interface{}{
		"task_id": "dependent", "depends_on_task_id": "predecessor", "coordinator_id": "forged",
	})
	require.True(t, result.IsError)
	require.NoError(t, database.Get(&edges, `SELECT COUNT(*) FROM task_blockers`))
	require.Equal(t, 1, edges)

	result = callTool(t, server, "add_task_dependency_kandev", args)
	require.False(t, result.IsError, firstText(t, result))
	require.NoError(t, database.Get(&revision, `SELECT dependency_revision FROM tasks WHERE id = 'dependent'`))
	require.Equal(t, 1, revision, "an audited replay must not advance the revision")
	require.NoError(t, database.Get(&audits, `SELECT COUNT(*) FROM task_dependency_mutation_audit WHERE outcome = 'succeeded'`))
	require.Equal(t, 2, audits)

	result = callTool(t, server, "add_task_dependency_kandev", map[string]interface{}{
		"task_id": "dependent", "depends_on_task_id": "unwatched",
	})
	require.True(t, result.IsError)
	require.NoError(t, database.Get(&edges, `SELECT COUNT(*) FROM task_blockers WHERE task_id = 'dependent'`))
	require.Equal(t, 1, edges)
	require.NoError(t, database.Get(&revision, `SELECT dependency_revision FROM tasks WHERE id = 'dependent'`))
	require.Equal(t, 1, revision)

	result = callTool(t, server, "remove_task_dependency_kandev", args)
	require.False(t, result.IsError, firstText(t, result))
	require.NoError(t, database.Get(&edges, `SELECT COUNT(*) FROM task_blockers WHERE task_id = 'dependent'`))
	require.Zero(t, edges)
	require.NoError(t, database.Get(&revision, `SELECT dependency_revision FROM tasks WHERE id = 'dependent'`))
	require.Equal(t, 2, revision)
	result = callTool(t, server, "remove_task_dependency_kandev", args)
	require.False(t, result.IsError, firstText(t, result))
	require.NoError(t, database.Get(&revision, `SELECT dependency_revision FROM tasks WHERE id = 'dependent'`))
	require.Equal(t, 2, revision)
	require.NoError(t, database.Get(&audits, `SELECT COUNT(*) FROM task_dependency_mutation_audit WHERE outcome = 'succeeded'`))
	require.Equal(t, 4, audits)
	ordinaryExecution := &lifecycle.AgentExecution{ID: "ordinary-execution", TaskID: "ordinary", SessionID: "ordinary-session"}
	ordinaryRequest, err := ws.NewRequest("ordinary-peer", ws.ActionMCPAddTaskDependency, args)
	require.NoError(t, err)
	ordinaryResponse, err := manager.MCPHandlerFor(ordinaryExecution).Dispatch(ctx, ordinaryRequest)
	require.NoError(t, err)
	require.Equal(t, ws.MessageTypeError, ordinaryResponse.Type)
	require.NoError(t, database.Get(&edges, `SELECT COUNT(*) FROM task_blockers`))
	require.Zero(t, edges)

	// An existing path through a hidden workflow must still prevent a cycle.
	_, err = database.ExecContext(ctx, `INSERT INTO task_blockers(task_id, blocker_task_id, created_at) VALUES('predecessor', 'unwatched', CURRENT_TIMESTAMP), ('unwatched', 'dependent', CURRENT_TIMESTAMP)`)
	require.NoError(t, err)
	result = callTool(t, server, "add_task_dependency_kandev", args)
	require.True(t, result.IsError)
	require.NoError(t, database.Get(&edges, `SELECT COUNT(*) FROM task_blockers`))
	require.Equal(t, 2, edges, "a hidden cycle denial must not write an edge")
	_, err = database.ExecContext(ctx, `DELETE FROM task_blockers WHERE task_id IN ('predecessor', 'unwatched')`)
	require.NoError(t, err)

	// The tool request may name endpoints, but the stream's stored session and
	// current Coordinator conversation determine the caller's authority.
	wrongSession := &lifecycle.AgentExecution{ID: "execution", TaskID: "conversation", SessionID: "missing-session"}
	wrongSession.SetMetadataForTesting(stored.Metadata)
	wrongSessionServer := NewWithProfile(NewDispatcherBackendClient(manager.MCPHandlerFor(wrongSession), log),
		"missing-session", "conversation", 10006, log, "", false, profile)
	t.Cleanup(func() { _ = wrongSessionServer.Close(context.Background()) })
	result = callTool(t, wrongSessionServer, "add_task_dependency_kandev", args)
	require.True(t, result.IsError)
	require.NoError(t, database.Get(&edges, `SELECT COUNT(*) FROM task_blockers`))
	require.Zero(t, edges)

	_, err = database.ExecContext(ctx, `UPDATE coordinators SET conversation_task_id = NULL WHERE id = ?`, coord.ID)
	require.NoError(t, err)
	result = callTool(t, server, "add_task_dependency_kandev", args)
	require.True(t, result.IsError)
	require.NoError(t, database.Get(&edges, `SELECT COUNT(*) FROM task_blockers`))
	require.Zero(t, edges)
}
