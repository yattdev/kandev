package handlers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/mcp/origin"
	"github.com/stretchr/testify/require"

	orchmodels "github.com/kandev/kandev/internal/office/models"
	taskmodels "github.com/kandev/kandev/internal/task/models"
	v1 "github.com/kandev/kandev/pkg/api/v1"
	ws "github.com/kandev/kandev/pkg/websocket"
)

func TestCoordinatorDependencyInvalidPayloadIsDeniedAndAudited(t *testing.T) {
	f := newSharedDependencyFixture(t)
	ctx := f.ctxFor(f.binding("add_task_dependency_kandev"), true)
	for _, payload := range []map[string]interface{}{
		{"task_id": 42, "depends_on_task_id": "predecessor"},
		{"task_id": "dependent", "depends_on_task_id": "predecessor", "extra": "private"},
	} {
		msg := makeWSMessage(t, ws.ActionMCPAddTaskDependency, payload)
		response, _, err := f.h.authorizeCoordinatorRequest(ctx, msg)
		require.NoError(t, err)
		assertWSError(t, response, ws.ErrorCodeBadRequest)
	}
	var denied int
	require.NoError(t, f.rawDB.Get(&denied,
		`SELECT COUNT(*) FROM task_dependency_mutation_audit WHERE outcome = 'denied' AND reason_code = 'invalid_payload'`))
	require.Equal(t, 2, denied)
	var edges int
	require.NoError(t, f.rawDB.Get(&edges, `SELECT COUNT(*) FROM task_blockers`))
	require.Zero(t, edges)
}

func TestCoordinatorDependencyAuditActionIsInternalOnly(t *testing.T) {
	f := newSharedDependencyFixture(t)
	ctx := f.ctxFor(f.binding("add_task_dependency_kandev"), true)
	msg := makeWSMessage(t, ws.ActionMCPAuditTaskDependencyAttempt, map[string]interface{}{
		"operation": "add", "dependent_id_present": true, "predecessor_id_present": false,
	})
	response, err := f.h.handleAuditTaskDependencyAttempt(ctx, msg)
	require.NoError(t, err)
	assertWSError(t, response, ws.ErrorCodeUnknownAction)
	response, err = f.h.handleAuditTaskDependencyAttempt(origin.WithTrustedInternalCall(ctx), msg)
	require.NoError(t, err)
	require.NotNil(t, response)
	var denied int
	require.NoError(t, f.rawDB.Get(&denied,
		`SELECT COUNT(*) FROM task_dependency_mutation_audit WHERE outcome = 'denied' AND reason_code = 'invalid_payload' AND dependent_id_present = 1 AND predecessor_id_present = 0`))
	require.Equal(t, 1, denied)
}

func TestCoordinatorDependencyMutationChecksBothWatchedEndpoints(t *testing.T) {
	f := newPhase2GuardFixture(t)
	seedWatchWorld(t, f)
	now := time.Now().UTC()
	require.NoError(t, f.repo.CreateTask(t.Context(), &taskmodels.Task{
		ID: "task-wf-a-peer", WorkspaceID: f.c.WorkspaceID, WorkflowID: "wf-a",
		Title: "peer", State: v1.TaskStateInProgress, CreatedAt: now, UpdatedAt: now,
	}))
	ctx := f.ctxFor(f.binding("add_task_dependency_kandev", "remove_task_dependency_kandev"), true)
	for _, action := range []string{ws.ActionMCPAddTaskDependency, ws.ActionMCPRemoveTaskDependency} {
		for _, tc := range []struct {
			name, dependent, predecessor string
			allowed                      bool
		}{
			{name: "both watched", dependent: "task-wf-a", predecessor: "task-wf-a-peer", allowed: true},
			{name: "dependent outside", dependent: "task-wf-b", predecessor: "task-wf-a-peer"},
			{name: "predecessor outside", dependent: "task-wf-a", predecessor: "task-wf-b"},
			{name: "missing predecessor", dependent: "task-wf-a", predecessor: "missing"},
		} {
			t.Run(action+"/"+tc.name, func(t *testing.T) {
				msg := makeWSMessage(t, action, map[string]interface{}{
					"task_id": tc.dependent, "depends_on_task_id": tc.predecessor,
				})
				guarded, replacement, err := f.h.authorizeCoordinatorRequest(ctx, msg)
				require.NoError(t, err)
				if tc.allowed {
					require.Nil(t, guarded)
					require.NotNil(t, replacement)
				} else {
					assertWSError(t, guarded, ws.ErrorCodeNotFound)
				}
			})
		}
	}
	var total, allowed int
	require.NoError(t, f.rawDB.Get(&total, `SELECT COUNT(*) FROM coordinator_activity WHERE coordinator_id = ? AND reason_code IN ('dependency_add', 'dependency_remove')`, f.c.ID))
	require.NoError(t, f.rawDB.Get(&allowed, `SELECT COUNT(*) FROM coordinator_activity WHERE coordinator_id = ? AND authorization = 'automatic' AND reason_code IN ('dependency_add', 'dependency_remove')`, f.c.ID))
	require.Equal(t, 8, total)
	require.Equal(t, 2, allowed)
	var denied int
	require.NoError(t, f.repo.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM task_dependency_mutation_audit WHERE outcome = 'denied'`).Scan(&denied))
	require.Equal(t, 6, denied)
}

func TestCoordinatorDependencyRechecksWatchBeforeMutation(t *testing.T) {
	f := newPhase2GuardFixture(t)
	seedWatchWorld(t, f)
	blockers := &memBlockerRepo{}
	f.h.taskSvc.SetBlockerRepository(blockers)
	now := time.Now().UTC()
	require.NoError(t, f.repo.CreateTask(t.Context(), &taskmodels.Task{
		ID: "task-wf-a-peer", WorkspaceID: f.c.WorkspaceID, WorkflowID: "wf-a",
		Title: "peer", State: v1.TaskStateInProgress, CreatedAt: now, UpdatedAt: now,
	}))
	ctx := f.ctxFor(f.binding("add_task_dependency_kandev"), true)
	msg := makeWSMessage(t, ws.ActionMCPAddTaskDependency, map[string]interface{}{
		"task_id": "task-wf-a", "depends_on_task_id": "task-wf-a-peer",
	})
	guarded, forwarded, err := f.h.authorizeCoordinatorRequest(ctx, msg)
	require.NoError(t, err)
	require.Nil(t, guarded)
	_, err = f.rawDB.ExecContext(ctx, `DELETE FROM coordinator_watches WHERE coordinator_id = ? AND workflow_id = ?`, f.c.ID, "wf-a")
	require.NoError(t, err)
	response, err := f.h.handleAddTaskDependency(ctx, forwarded)
	require.NoError(t, err)
	assertWSError(t, response, ws.ErrorCodeNotFound)
	require.Empty(t, blockers.edges)
}

func TestCoordinatorDependencyCycleErrorOmitsTaskPath(t *testing.T) {
	f := newSharedDependencyFixture(t)
	_, err := f.rawDB.Exec(`INSERT INTO task_blockers(task_id, blocker_task_id, created_at) VALUES('predecessor', 'dependent', CURRENT_TIMESTAMP)`)
	require.NoError(t, err)
	ctx := f.ctxFor(f.binding("add_task_dependency_kandev"), true)
	msg := makeWSMessage(t, ws.ActionMCPAddTaskDependency, map[string]interface{}{
		"task_id": "dependent", "depends_on_task_id": "predecessor",
	})
	guarded, forwarded, err := f.h.authorizeCoordinatorRequest(ctx, msg)
	require.NoError(t, err)
	require.Nil(t, guarded)
	response, err := f.h.handleAddTaskDependency(ctx, forwarded)
	require.NoError(t, err)
	assertWSError(t, response, ws.ErrorCodeNotFound)
	require.False(t, strings.Contains(string(response.Payload), "dependent"))
	var edges int
	require.NoError(t, f.rawDB.Get(&edges, `SELECT COUNT(*) FROM task_blockers`))
	require.Equal(t, 1, edges)
}

func TestCoordinatorDependencySelfEdgeIsAudited(t *testing.T) {
	f := newSharedDependencyFixture(t)
	ctx := f.ctxFor(f.binding("add_task_dependency_kandev"), true)
	msg := makeWSMessage(t, ws.ActionMCPAddTaskDependency, map[string]interface{}{
		"task_id": "dependent", "depends_on_task_id": "dependent",
	})
	guarded, forwarded, err := f.h.authorizeCoordinatorRequest(ctx, msg)
	require.NoError(t, err)
	require.Nil(t, guarded)
	response, err := f.h.handleAddTaskDependency(ctx, forwarded)
	require.NoError(t, err)
	assertWSError(t, response, ws.ErrorCodeValidation)
	var denied int
	require.NoError(t, f.rawDB.Get(&denied,
		`SELECT COUNT(*) FROM task_dependency_mutation_audit WHERE outcome = 'denied' AND reason_code = 'invalid_edge' AND dependent_id_present = 1 AND predecessor_id_present = 1`))
	require.Equal(t, 1, denied)
}

func TestCoordinatorDependencyCycleThroughUnwatchedTaskIsNotFound(t *testing.T) {
	f := newSharedDependencyFixture(t)
	_, err := f.rawDB.Exec(`INSERT INTO task_blockers(task_id, blocker_task_id, created_at) VALUES
		('predecessor', 'unwatched', CURRENT_TIMESTAMP), ('unwatched', 'dependent', CURRENT_TIMESTAMP)`)
	require.NoError(t, err)
	ctx := f.ctxFor(f.binding("add_task_dependency_kandev"), true)
	msg := makeWSMessage(t, ws.ActionMCPAddTaskDependency, map[string]interface{}{
		"task_id": "dependent", "depends_on_task_id": "predecessor",
	})
	guarded, forwarded, err := f.h.authorizeCoordinatorRequest(ctx, msg)
	require.NoError(t, err)
	require.Nil(t, guarded)
	response, err := f.h.handleAddTaskDependency(ctx, forwarded)
	require.NoError(t, err)
	assertWSError(t, response, ws.ErrorCodeNotFound)
	require.False(t, strings.Contains(string(response.Payload), "unwatched"))
}

func TestCoordinatorDependencyAuditFailurePreventsMutation(t *testing.T) {
	f := newPhase2GuardFixture(t)
	seedWatchWorld(t, f)
	ctx := f.ctxFor(f.binding("add_task_dependency_kandev"), true)
	_, err := f.rawDB.Exec(`DROP TABLE coordinator_activity`)
	require.NoError(t, err)
	msg := makeWSMessage(t, ws.ActionMCPAddTaskDependency, map[string]interface{}{
		"task_id": "task-wf-a", "depends_on_task_id": "task-wf-b",
	})
	guarded, _, err := f.h.authorizeCoordinatorRequest(ctx, msg)
	require.NoError(t, err)
	assertWSError(t, guarded, ws.ErrorCodeInternalError)
}

func TestCoordinatorDependencyMalformedPayloadIsAudited(t *testing.T) {
	f := newPhase2GuardFixture(t)
	seedWatchWorld(t, f)
	ctx := f.ctxFor(f.binding("add_task_dependency_kandev"), true)
	msg := &ws.Message{ID: "malformed-dependency", Action: ws.ActionMCPAddTaskDependency, Payload: json.RawMessage(`{"task_id":`)}
	guarded, _, err := f.h.authorizeCoordinatorRequest(ctx, msg)
	require.NoError(t, err)
	assertWSError(t, guarded, ws.ErrorCodeBadRequest)
	var count int
	require.NoError(t, f.rawDB.Get(&count, `SELECT COUNT(*) FROM coordinator_activity WHERE coordinator_id = ? AND reason_code = 'dependency_add' AND authorization = 'denied'`, f.c.ID))
	require.Equal(t, 1, count)
	var durable int
	require.NoError(t, f.repo.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM task_dependency_mutation_audit WHERE outcome = 'denied' AND reason_code = 'invalid_payload'`).Scan(&durable))
	require.Equal(t, 1, durable)
}

func TestCoordinatorDependencyReadbackFiltersUnwatchedEdge(t *testing.T) {
	f := newPhase2GuardFixture(t)
	seedWatchWorld(t, f)
	blockers := &memBlockerRepo{}
	f.h.taskSvc.SetBlockerRepository(blockers)
	require.NoError(t, blockers.CreateTaskBlocker(t.Context(), &orchmodels.TaskBlocker{
		TaskID: "task-wf-a", BlockerTaskID: "task-wf-b",
	}))
	ctx := f.ctxFor(nil, false)
	msg := makeWSMessage(t, ws.ActionMCPAddTaskDependency, map[string]interface{}{})
	response, err := f.h.dependencyResult(ctx, msg, "task-wf-a")
	require.NoError(t, err)
	require.Equal(t, ws.MessageTypeResponse, response.Type)
	var body struct {
		DependsOn []string `json:"depends_on"`
	}
	require.NoError(t, json.Unmarshal(response.Payload, &body))
	require.Empty(t, body.DependsOn)
}

func TestCoordinatorDependencyReadbackRejectsUnwatchedDependent(t *testing.T) {
	f := newPhase2GuardFixture(t)
	seedWatchWorld(t, f)
	ctx := f.ctxFor(nil, false)
	msg := makeWSMessage(t, ws.ActionMCPAddTaskDependency, map[string]interface{}{})
	response, err := f.h.dependencyResult(ctx, msg, "task-wf-b")
	require.NoError(t, err)
	assertWSError(t, response, ws.ErrorCodeNotFound)
}

func TestCoordinatorDependencyMutationRemovesCompletedPredecessor(t *testing.T) {
	f := newSharedDependencyFixture(t)
	now := time.Now().UTC()
	require.NoError(t, f.repo.CreateTask(context.Background(), &taskmodels.Task{
		ID: "completed-predecessor", WorkspaceID: f.c.WorkspaceID, WorkflowID: "wf-a",
		Title: "completed", State: v1.TaskStateCompleted, CreatedAt: now, UpdatedAt: now,
	}))
	ctx := f.ctxFor(f.binding("add_task_dependency_kandev", "remove_task_dependency_kandev"), true)
	for _, action := range []string{ws.ActionMCPAddTaskDependency, ws.ActionMCPRemoveTaskDependency} {
		msg := makeWSMessage(t, action, map[string]interface{}{
			"task_id": "dependent", "depends_on_task_id": "completed-predecessor",
		})
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
		require.Equal(t, ws.MessageTypeResponse, response.Type, "%s", response.Payload)
	}
	var edges int
	require.NoError(t, f.rawDB.Get(&edges, `SELECT COUNT(*) FROM task_blockers WHERE task_id = 'dependent'`))
	require.Zero(t, edges)
}
