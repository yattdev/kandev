package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"

	"github.com/kandev/kandev/internal/agentctl/types/streams"
	"github.com/kandev/kandev/internal/coordinator"
	"github.com/kandev/kandev/internal/events"
	"github.com/kandev/kandev/internal/events/bus"
	mcpprofile "github.com/kandev/kandev/internal/mcp/profile"
	"github.com/kandev/kandev/internal/mcp/scope"
	officesqlite "github.com/kandev/kandev/internal/office/repository/sqlite"
	"github.com/kandev/kandev/internal/task/dependencywrite"
	taskmodels "github.com/kandev/kandev/internal/task/models"
	v1 "github.com/kandev/kandev/pkg/api/v1"
	ws "github.com/kandev/kandev/pkg/websocket"
)

func TestCoordinatorDependencyReadbackRechecksEndpointWorkflowAfterMove(t *testing.T) {
	f := newSharedDependencyFixture(t)
	ctx := f.ctxFor(f.binding("add_task_dependency_kandev"), true)
	result, err := f.repo.MutateDependency(ctx, dependencywrite.DependencyMutationRequest{
		Operation: "add", WorkspaceID: f.c.WorkspaceID,
		DependentTaskID: "dependent", PredecessorTaskID: "predecessor",
		OriginClass: "task_service", CorrelationID: "readback-race",
	}, func(context.Context, *sqlx.Tx) error { return nil })
	require.NoError(t, err)
	_, err = f.rawDB.ExecContext(ctx, `UPDATE tasks SET workflow_id = 'wf-b' WHERE id = 'predecessor'`)
	require.NoError(t, err)
	principal, ok := scope.PrincipalFromContext(ctx)
	require.True(t, ok)
	_, _, err = f.h.coordinatorDependencySnapshots(ctx, principal, result)
	require.ErrorIs(t, err, coordinator.ErrNotFound)
}

func TestCoordinatorDependencyReadbackFiltersMovedRelation(t *testing.T) {
	f := newSharedDependencyFixture(t)
	ctx := f.ctxFor(f.binding("add_task_dependency_kandev"), true)
	now := time.Now().UTC()
	require.NoError(t, f.repo.CreateTask(ctx, &taskmodels.Task{
		ID: "related", WorkspaceID: f.c.WorkspaceID, WorkflowID: "wf-a",
		Title: "related", State: v1.TaskStateInProgress, CreatedAt: now, UpdatedAt: now,
	}))
	_, err := f.rawDB.ExecContext(ctx, `INSERT INTO task_blockers(task_id, blocker_task_id, created_at)
		VALUES('dependent', 'related', CURRENT_TIMESTAMP)`)
	require.NoError(t, err)
	result, err := f.repo.MutateDependency(ctx, dependencywrite.DependencyMutationRequest{
		Operation: "add", WorkspaceID: f.c.WorkspaceID,
		DependentTaskID: "dependent", PredecessorTaskID: "predecessor",
		OriginClass: "task_service", CorrelationID: "relation-readback-race",
	}, func(context.Context, *sqlx.Tx) error { return nil })
	require.NoError(t, err)
	_, err = f.rawDB.ExecContext(ctx, `UPDATE tasks SET workflow_id = 'wf-b' WHERE id = 'related'`)
	require.NoError(t, err)
	principal, ok := scope.PrincipalFromContext(ctx)
	require.True(t, ok)
	dependent, _, err := f.h.coordinatorDependencySnapshots(ctx, principal, result)
	require.NoError(t, err)
	encoded, err := json.Marshal(dependent)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "related")
}

func newSharedDependencyFixture(t *testing.T) *guardFixture {
	t.Helper()
	taskSvc, repo, eventBus := newTestTaskServiceWithEventBus(t)
	shared := sqlx.NewDb(repo.DB(), "sqlite3")
	officeRepo, err := officesqlite.NewWithDB(shared, shared, nil)
	require.NoError(t, err)
	taskSvc.SetBlockerRepository(officeRepo)
	store, err := coordinator.NewStore(shared, shared)
	require.NoError(t, err)
	svc := coordinator.NewService(store, coordinator.NewValidator(nil, nil), allowAllAuthorizer{},
		testLogger(t), coordinator.WithPhase2(true))
	h := &Handlers{taskSvc: taskSvc, logger: testLogger(t).WithFields()}
	h.SetCoordinatorService(svc)
	workspaces, err := taskSvc.ListWorkspaces(t.Context())
	require.NoError(t, err)
	require.Len(t, workspaces, 1)
	c := &coordinator.Coordinator{WorkspaceID: workspaces[0].ID, Name: "coordinator", AgentProfileID: "ap", ExecutorProfileID: "ep"}
	require.NoError(t, store.CreateCoordinator(t.Context(), c))
	principal := coordinatorTestPrincipal(c.WorkspaceID)
	for _, workflowID := range []string{"wf-a", "wf-b"} {
		require.NoError(t, repo.CreateWorkflow(t.Context(), &taskmodels.Workflow{ID: workflowID, WorkspaceID: c.WorkspaceID, Name: workflowID}))
	}
	now := time.Now().UTC()
	for _, entry := range []struct{ ID, Workflow string }{
		{"dependent", "wf-a"}, {"predecessor", "wf-a"}, {"unwatched", "wf-b"},
	} {
		require.NoError(t, repo.CreateTask(t.Context(), &taskmodels.Task{
			ID: entry.ID, WorkspaceID: c.WorkspaceID, WorkflowID: entry.Workflow,
			Title: entry.ID, State: v1.TaskStateInProgress, CreatedAt: now, UpdatedAt: now,
		}))
	}
	_, err = svc.SaveSettings(t.Context(), c.WorkspaceID, c.ID,
		[]byte(`{"watches":{"scope":"selected","workflow_ids":["wf-a"]}}`))
	require.NoError(t, err)
	require.NoError(t, repo.CreateTask(t.Context(), &taskmodels.Task{
		ID: principal.CallerTaskID, WorkspaceID: c.WorkspaceID, WorkflowID: "wf-a",
		Title: "conversation", State: v1.TaskStateInProgress, CreatedAt: now, UpdatedAt: now,
	}))
	_, err = shared.ExecContext(t.Context(), `INSERT INTO task_sessions(id, task_id, state, started_at, updated_at)
		VALUES(?, ?, 'RUNNING', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, principal.CallerSessionID, principal.CallerTaskID)
	require.NoError(t, err)
	_, err = shared.ExecContext(t.Context(), `INSERT INTO executors_running(id, session_id, task_id, executor_id, status,
		agent_execution_id, created_at, updated_at) VALUES('executor', ?, ?, 'local', 'running', 'exec-1', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		principal.CallerSessionID, principal.CallerTaskID)
	require.NoError(t, err)
	_, err = shared.ExecContext(t.Context(), `UPDATE coordinators SET conversation_task_id = ? WHERE id = ?`, principal.CallerTaskID, c.ID)
	require.NoError(t, err)
	f := &guardFixture{h: h, svc: svc, c: c, rawDB: shared, repo: repo, store: store, events: eventBus}
	f.ctxFor = func(binding *mcpprofile.CoordinatorToolPolicy, required bool) context.Context {
		p := principal
		p.CoordinatorID = c.ID
		ctx := scope.WithPrincipal(context.Background(), p)
		return streams.WithMCPExecutionContext(ctx, streams.MCPExecutionContext{
			ExecutionID: "exec-1", TaskID: p.CallerTaskID, SessionID: p.CallerSessionID,
			CoordinatorToolPolicy: binding, CoordinatorToolPolicyRequired: required,
		})
	}
	return f
}

func TestCoordinatorDependencyHandlerCommitsAndAuditsChangedAndNoop(t *testing.T) {
	f := newSharedDependencyFixture(t)
	var publications []map[string]interface{}
	subscription, err := f.events.Subscribe(events.TaskUpdated, func(_ context.Context, event *bus.Event) error {
		payload, ok := event.Data.(map[string]interface{})
		if ok {
			publications = append(publications, payload)
		}
		return nil
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = subscription.Unsubscribe() })
	ctx := f.ctxFor(f.binding("add_task_dependency_kandev", "remove_task_dependency_kandev"), true)
	for index, action := range []string{ws.ActionMCPAddTaskDependency, ws.ActionMCPAddTaskDependency,
		ws.ActionMCPRemoveTaskDependency, ws.ActionMCPRemoveTaskDependency} {
		msg := makeWSMessage(t, action, map[string]interface{}{
			"task_id": "dependent", "depends_on_task_id": "predecessor",
		})
		msg.ID = fmt.Sprintf("request-%d", index)
		guarded, forwarded, err := f.h.authorizeCoordinatorRequest(ctx, msg)
		require.NoError(t, err)
		require.Nil(t, guarded)
		var response *ws.Message
		if action == ws.ActionMCPAddTaskDependency {
			response, err = f.h.handleAddTaskDependency(ctx, forwarded)
		} else {
			response, err = f.h.handleRemoveTaskDependency(ctx, forwarded)
		}
		require.NoError(t, err)
		require.Equal(t, ws.MessageTypeResponse, response.Type, string(response.Payload))
		var body struct {
			Changed           bool  `json:"changed"`
			DependentRevision int64 `json:"dependent_revision"`
		}
		require.NoError(t, json.Unmarshal(response.Payload, &body))
		require.Equal(t, index%2 == 0, body.Changed)
		require.EqualValues(t, index/2+1, body.DependentRevision)
	}
	var edges, audits int
	require.NoError(t, f.rawDB.Get(&edges, `SELECT COUNT(*) FROM task_blockers WHERE task_id = 'dependent'`))
	require.NoError(t, f.rawDB.Get(&audits, `SELECT COUNT(*) FROM task_dependency_mutation_audit WHERE outcome = 'succeeded'`))
	require.Zero(t, edges)
	require.Equal(t, 4, audits)
	require.Len(t, publications, 4)
	require.EqualValues(t, 1, publications[0]["dependency_revision"])
	require.Len(t, publications[0]["depends_on"], 1)
	require.EqualValues(t, 2, publications[2]["dependency_revision"])
	require.Empty(t, publications[2]["depends_on"])
}

func TestCoordinatorDependencyHandlerRejectsStaleCallerAtWriteBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, update string
	}{
		{"stopped executor", `UPDATE executors_running SET status = 'stopped' WHERE id = 'executor'`},
		{"replaced execution", `UPDATE executors_running SET agent_execution_id = 'next-exec' WHERE id = 'executor'`},
		{"finished session", `UPDATE task_sessions SET state = 'COMPLETED' WHERE id = 'coordinator-session'`},
		{"reassigned conversation", `UPDATE coordinators SET conversation_task_id = NULL`},
		{"watch removed", `DELETE FROM coordinator_watches WHERE workflow_id = 'wf-a'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSharedDependencyFixture(t)
			ctx := f.ctxFor(f.binding("add_task_dependency_kandev"), true)
			msg := makeWSMessage(t, ws.ActionMCPAddTaskDependency, map[string]interface{}{
				"task_id": "dependent", "depends_on_task_id": "predecessor",
			})
			guarded, forwarded, err := f.h.authorizeCoordinatorRequest(ctx, msg)
			require.NoError(t, err)
			require.Nil(t, guarded)
			_, err = f.rawDB.ExecContext(ctx, tc.update)
			require.NoError(t, err)
			response, err := f.h.handleAddTaskDependency(ctx, forwarded)
			require.NoError(t, err)
			assertWSError(t, response, ws.ErrorCodeNotFound)
			var edges, denials int
			require.NoError(t, f.rawDB.Get(&edges, `SELECT COUNT(*) FROM task_blockers`))
			require.NoError(t, f.rawDB.Get(&denials, `SELECT COUNT(*) FROM task_dependency_mutation_audit WHERE outcome = 'denied'`))
			require.Zero(t, edges)
			require.Equal(t, 1, denials)
		})
	}
}

func TestCoordinatorDependencyHandlerUsesCurrentProjectMembership(t *testing.T) {
	f := newSharedDependencyFixture(t)
	ctx := t.Context()
	svc := coordinator.NewService(f.store, coordinator.NewValidator(nil, nil), allowAllAuthorizer{},
		testLogger(t), coordinator.WithPhase2(true), coordinator.WithPhase31(true))
	svc.SetProjectReader(f.h.taskSvc)
	f.h.SetCoordinatorService(svc)
	f.svc = svc
	for _, repoID := range []string{"repo-in", "repo-out"} {
		require.NoError(t, f.repo.CreateRepository(ctx, &taskmodels.Repository{
			ID: repoID, WorkspaceID: f.c.WorkspaceID, Name: repoID,
			SourceType: "local", LocalPath: "/tmp/" + repoID,
		}))
	}
	for taskID, repoID := range map[string]string{"dependent": "repo-in", "predecessor": "repo-out"} {
		require.NoError(t, f.repo.CreateTaskRepository(ctx, &taskmodels.TaskRepository{
			TaskID: taskID, RepositoryID: repoID, BaseBranch: "main",
		}))
	}
	_, err := f.rawDB.ExecContext(ctx, `UPDATE coordinators SET conversation_task_id = NULL WHERE id = ?`, f.c.ID)
	require.NoError(t, err)
	_, err = svc.SaveSettings(ctx, f.c.WorkspaceID, f.c.ID, []byte(`{
		"watches":{"scope":"selected","workflow_ids":["wf-a"]},
		"projects":{"scope":"selected","entries":[{"kind":"repository","id":"repo-in"}]}}`))
	require.NoError(t, err)
	principal := coordinatorTestPrincipal(f.c.WorkspaceID)
	_, err = f.rawDB.ExecContext(ctx, `UPDATE coordinators SET conversation_task_id = ? WHERE id = ?`, principal.CallerTaskID, f.c.ID)
	require.NoError(t, err)
	callCtx := f.ctxFor(f.binding("add_task_dependency_kandev"), true)
	msg := makeWSMessage(t, ws.ActionMCPAddTaskDependency, map[string]interface{}{
		"task_id": "dependent", "depends_on_task_id": "predecessor",
	})
	response, err := f.h.handleAddTaskDependency(callCtx, msg)
	require.NoError(t, err)
	assertWSError(t, response, ws.ErrorCodeNotFound)
	var edges int
	require.NoError(t, f.rawDB.Get(&edges, `SELECT COUNT(*) FROM task_blockers`))
	require.Zero(t, edges)
	_, err = f.rawDB.ExecContext(ctx, `UPDATE task_repositories SET repository_id = 'repo-in' WHERE task_id = 'predecessor'`)
	require.NoError(t, err)
	response, err = f.h.handleAddTaskDependency(callCtx, msg)
	require.NoError(t, err)
	require.Equal(t, ws.MessageTypeResponse, response.Type, string(response.Payload))
}

func TestCoordinatorDependencyReadbackHidesUnwatchedNeighbor(t *testing.T) {
	f := newSharedDependencyFixture(t)
	_, err := f.rawDB.ExecContext(t.Context(), `INSERT INTO task_blockers(task_id, blocker_task_id, created_at)
		VALUES('dependent', 'unwatched', CURRENT_TIMESTAMP)`)
	require.NoError(t, err)
	ctx := f.ctxFor(f.binding("add_task_dependency_kandev"), true)
	msg := makeWSMessage(t, ws.ActionMCPAddTaskDependency, map[string]interface{}{
		"task_id": "dependent", "depends_on_task_id": "predecessor",
	})
	response, err := f.h.handleAddTaskDependency(ctx, msg)
	require.NoError(t, err)
	require.Equal(t, ws.MessageTypeResponse, response.Type, string(response.Payload))
	require.NotContains(t, string(response.Payload), "unwatched")
	var body struct {
		Dependent struct {
			BlockedReason string `json:"blocked_reason"`
			DependsOn     []struct {
				ID string `json:"id"`
			} `json:"depends_on"`
		} `json:"dependent"`
	}
	require.NoError(t, json.Unmarshal(response.Payload, &body))
	require.Equal(t, "unknown", body.Dependent.BlockedReason)
	require.Equal(t, "predecessor", body.Dependent.DependsOn[0].ID)
}
