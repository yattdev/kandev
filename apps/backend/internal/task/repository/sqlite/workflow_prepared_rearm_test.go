package sqlite

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/testutil"
	v1 "github.com/kandev/kandev/pkg/api/v1"
	"github.com/stretchr/testify/require"
)

func seedPreparedWorkflowRearm(t *testing.T, repo *Repository) (string, models.WorkflowSessionRoute) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "ws-1", Name: "prepared workflow fixture"}))
	sourceID, destinationID := uuid.NewString(), uuid.NewString()
	task := createStepTransitionsTestTask(t, repo, "retry-task", "wf1", "source-step")
	task.WorkflowStepID, task.State = "destination-step", v1.TaskStateFailed
	require.NoError(t, repo.UpdateTask(ctx, task))
	require.NoError(t, repo.CreateTaskEnvironment(ctx, &models.TaskEnvironment{
		ID: "env", TaskID: task.ID, ExecutorType: string(models.ExecutorTypeLocal), Status: models.TaskEnvironmentStatusReady,
	}))
	require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{ID: sourceID, TaskID: task.ID,
		AgentProfileID: "profile-a", State: models.TaskSessionStateWaitingForInput, IsPrimary: true, TaskEnvironmentID: "env",
		StartedAt: time.Now().UTC()}))
	require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{ID: destinationID, TaskID: task.ID,
		AgentProfileID: "profile-b", State: models.TaskSessionStateFailed, TaskEnvironmentID: "env",
		ErrorMessage: "environment preparation failed: base branch does not exist", StartedAt: time.Now().UTC()}))
	latest, err := repo.GetLatestTaskStepTransitionID(ctx, task.ID)
	require.NoError(t, err)
	route := models.WorkflowSessionRoute{OperationID: "retry", DestinationStepID: task.WorkflowStepID,
		EntryIdentity: fmt.Sprintf("entry:%020d", latest), TargetKind: "profile", AgentProfileID: "profile-b",
		SourceSessionID: sourceID, DestinationID: destinationID, Phase: "prepared"}
	require.NoError(t, repo.SetTaskMetadataKey(ctx, task.ID, models.MetaKeyWorkflowSessionRoute, route))
	return task.ID, route
}

func TestPreparedWorkflowRearmAtomic(t *testing.T) {
	repo := newStepTransitionsTestRepo(t)
	testPreparedWorkflowRearmAtomic(t, repo)
}

func testPreparedWorkflowRearmAtomic(t *testing.T, repo *Repository) {
	t.Helper()
	ctx := context.Background()
	taskID, route := seedPreparedWorkflowRearm(t, repo)
	changed, err := repo.RearmPreparedWorkflowSession(ctx, taskID, route)
	require.NoError(t, err)
	require.True(t, changed)
	session, err := repo.GetTaskSession(ctx, route.DestinationID)
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateCreated, session.State)
	require.Empty(t, session.ErrorMessage)
	current, err := repo.GetTask(ctx, taskID)
	require.NoError(t, err)
	require.Equal(t, v1.TaskStateScheduling, current.State)
}

func TestPreparedWorkflowRearmRollsBackBothStates(t *testing.T) {
	repo := newStepTransitionsTestRepo(t)
	taskID, route := seedPreparedWorkflowRearm(t, repo)
	ctx := context.Background()
	_, err := repo.DB().ExecContext(ctx, `CREATE TRIGGER refuse_retry BEFORE UPDATE OF state ON tasks
 WHEN NEW.state = 'SCHEDULING' BEGIN SELECT RAISE(ABORT, 'retry refused'); END`)
	require.NoError(t, err)
	changed, err := repo.RearmPreparedWorkflowSession(ctx, taskID, route)
	require.ErrorContains(t, err, "retry refused")
	require.False(t, changed)
	task, err := repo.GetTask(ctx, taskID)
	require.NoError(t, err)
	require.Equal(t, v1.TaskStateFailed, task.State)
	destination, err := repo.GetTaskSession(ctx, route.DestinationID)
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateFailed, destination.State)
	require.Equal(t, "environment preparation failed: base branch does not exist", destination.ErrorMessage)
}

func TestPreparedWorkflowRearmRejectsRuntimeOrDelivery(t *testing.T) {
	for _, scenario := range []string{"turn", "generation", "submission", "block", "stale route", "stale transition"} {
		t.Run(scenario, func(t *testing.T) {
			repo := newStepTransitionsTestRepo(t)
			taskID, route := seedPreparedWorkflowRearm(t, repo)
			ctx := context.Background()
			now := time.Now().UTC()
			switch scenario {
			case "turn":
				require.NoError(t, repo.CreateTurn(ctx, &models.Turn{ID: "first", TaskID: taskID, TaskSessionID: route.DestinationID}))
			case "generation":
				_, err := repo.DB().ExecContext(ctx, `INSERT INTO harness_session_generations
     (session_id, incarnation_id, generation, native_session_id, created_at, committed_at) VALUES (?, ?, ?, ?, ?, ?)`,
					route.DestinationID, "incarnation", 1, "native", now, now)
				require.NoError(t, err)
			case "submission":
				_, err := repo.DB().ExecContext(ctx, `INSERT INTO agent_delivery_submissions
     (id, session_id, incarnation_id, harness_generation, owner_generation, payload_hash, payload, state, created_at, updated_at)
     VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, "submission", route.DestinationID, "incarnation", 1, 1, "hash", []byte("prompt"), "pending", now, now)
				require.NoError(t, err)
			case "block":
				_, err := repo.DB().ExecContext(ctx, `INSERT INTO session_recovery_blocks
     (id, session_id, incarnation_id, expected_generation, reason, state, created_at, updated_at)
     VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, "block", route.DestinationID, "incarnation", 1, "unknown", "open", now, now)
				require.NoError(t, err)
			case "stale route":
				replacement := route
				replacement.OperationID = "new-route"
				require.NoError(t, repo.SetTaskMetadataKey(ctx, taskID, models.MetaKeyWorkflowSessionRoute, replacement))
			case "stale transition":
				task, err := repo.GetTask(ctx, taskID)
				require.NoError(t, err)
				task.WorkflowStepID = "new-step"
				require.NoError(t, repo.UpdateTask(ctx, task))
			}
			changed, err := repo.RearmPreparedWorkflowSession(ctx, taskID, route)
			require.NoError(t, err)
			require.False(t, changed)
			destination, err := repo.GetTaskSession(ctx, route.DestinationID)
			require.NoError(t, err)
			require.Equal(t, models.TaskSessionStateFailed, destination.State)
		})
	}
}

func TestPreparedWorkflowRearmAtomicPostgres(t *testing.T) {
	database := openIsolatedPostgresMultiConn(t, testutil.PostgresDSNFromEnv(t), 4)
	repo, err := NewWithDB(database, database, nil)
	require.NoError(t, err)
	testPreparedWorkflowRearmAtomic(t, repo)
}
