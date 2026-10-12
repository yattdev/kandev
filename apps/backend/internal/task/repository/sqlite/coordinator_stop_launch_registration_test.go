package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/agentruntime"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/testutil"
	"github.com/stretchr/testify/require"
)

// @covers AC-STOP-FENCE-002, AC-STOP-FENCE-007, AC-STOP-FENCE-008
func TestCoordinatorStopPendingLaunchRequestRejectsRegistration(t *testing.T) {
	assertCoordinatorStopPendingLaunchRegistration(t, newRepoForSessionTests(t))
}

// @covers AC-STOP-FENCE-002, AC-STOP-FENCE-007, AC-STOP-FENCE-008
func TestPostgresCoordinatorStopPendingLaunchRequestRejectsRegistration(t *testing.T) {
	db := testutil.OpenIsolatedPostgres(t, testutil.PostgresDSNFromEnv(t))
	repo, err := NewWithDB(db, db, nil)
	require.NoError(t, err)
	assertCoordinatorStopPendingLaunchRegistration(t, repo)
}

// @covers AC-STOP-FENCE-003, AC-STOP-FENCE-007
func TestCoordinatorStopLaunchFenceRequiresCapturedTurnAndQueue(t *testing.T) {
	assertCoordinatorStopLaunchFenceIdentity(t, newRepoForSessionTests(t))
}

// @covers AC-STOP-FENCE-003, AC-STOP-FENCE-007, AC-STOP-FENCE-008
func TestPostgresCoordinatorStopLaunchFenceRequiresCapturedTurnAndQueue(t *testing.T) {
	db := testutil.OpenIsolatedPostgres(t, testutil.PostgresDSNFromEnv(t))
	repo, err := NewWithDB(db, db, nil)
	require.NoError(t, err)
	assertCoordinatorStopLaunchFenceIdentity(t, repo)
}

// @covers AC-STOP-FENCE-005, AC-STOP-FENCE-007
func TestCoordinatorStopBoundLaunchFenceRepeatPreservesSuccessor(t *testing.T) {
	assertCoordinatorStopBoundLaunchFenceRepeat(t, newRepoForSessionTests(t))
}

// @covers AC-STOP-FENCE-005, AC-STOP-FENCE-007, AC-STOP-FENCE-008
func TestPostgresCoordinatorStopBoundLaunchFenceRepeatPreservesSuccessor(t *testing.T) {
	db := testutil.OpenIsolatedPostgres(t, testutil.PostgresDSNFromEnv(t))
	repo, err := NewWithDB(db, db, nil)
	require.NoError(t, err)
	assertCoordinatorStopBoundLaunchFenceRepeat(t, repo)
}

func assertCoordinatorStopBoundLaunchFenceRepeat(t *testing.T, repo *Repository) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: "bound-launch-task", Title: "launch"}))
	require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: "bound-launch-parent", Title: "parent"}))
	require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{ID: "bound-launch-session", TaskID: "bound-launch-task", State: models.TaskSessionStateStarting}))
	request := models.CoordinatorStopRequest{TaskID: "bound-launch-task", ParentTaskID: "bound-launch-parent", OperationID: "bound-launch-stop"}
	_, _, _, err := repo.CaptureCoordinatorStopRequestWithCandidates(ctx, request)
	require.NoError(t, err)
	changed, err := repo.FenceCoordinatorStopSessionForRequest(ctx, request.TaskID, "bound-launch-session", request.OperationID)
	require.NoError(t, err)
	require.True(t, changed)
	fences, err := repo.ListCoordinatorStopRequestSessionFences(ctx, request.TaskID, request.OperationID)
	require.NoError(t, err)
	require.NoError(t, repo.UpdateTaskSessionState(ctx, "bound-launch-session", models.TaskSessionStateStarting, ""))
	before, err := repo.GetTaskSession(ctx, "bound-launch-session")
	require.NoError(t, err)
	changed, err = repo.FenceCoordinatorStopSessionForRequest(ctx, request.TaskID, "bound-launch-session", request.OperationID)
	require.NoError(t, err)
	require.False(t, changed, "a persisted request fence cannot cancel an authorized successor")
	after, err := repo.GetTaskSession(ctx, "bound-launch-session")
	require.NoError(t, err)
	require.Equal(t, before, after)
	retryFences, err := repo.ListCoordinatorStopRequestSessionFences(ctx, request.TaskID, request.OperationID)
	require.NoError(t, err)
	require.Equal(t, fences, retryFences)
}

func assertCoordinatorStopLaunchFenceIdentity(t *testing.T, repo *Repository) {
	t.Helper()
	ctx := context.Background()
	for _, test := range []string{"matching", "lifecycle sibling", "preexisting lifecycle sibling", "successor turn", "successor queue", "missing turn"} {
		t.Run(test, func(t *testing.T) {
			now := time.Now().UTC()
			taskID, sessionID, turnID := "task-"+test, "session-"+test, "turn-"+test
			require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, Title: "launch"}))
			require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: "parent-" + test, Title: "parent"}))
			require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{ID: sessionID, TaskID: taskID, State: models.TaskSessionStateStarting, StartedAt: now, UpdatedAt: now}))
			require.NoError(t, repo.CreateTurn(ctx, &models.Turn{ID: turnID, TaskID: taskID, TaskSessionID: sessionID, StartedAt: now, CreatedAt: now, UpdatedAt: now}))
			if test == "preexisting lifecycle sibling" {
				createCoordinatorLaunchLifecycleTurn(t, repo, taskID, sessionID, turnID, now)
			}
			request := models.CoordinatorStopRequest{TaskID: taskID, ParentTaskID: "parent-" + test, OperationID: "launch-stop"}
			_, candidates, _, err := repo.CaptureCoordinatorStopRequestWithCandidates(ctx, request)
			require.NoError(t, err)
			require.Equal(t, turnID, candidates[0].TurnID)
			switch test {
			case "lifecycle sibling":
				createCoordinatorLaunchLifecycleTurn(t, repo, taskID, sessionID, turnID, now)
			case "successor turn":
				_, err = repo.db.ExecContext(ctx, repo.db.Rebind(`UPDATE task_session_turns SET completed_at = ? WHERE id = ?`), now, turnID)
				require.NoError(t, err)
				require.NoError(t, repo.CreateTurn(ctx, &models.Turn{ID: "successor-" + turnID, TaskID: taskID, TaskSessionID: sessionID, StartedAt: now.Add(time.Second), CreatedAt: now, UpdatedAt: now}))
			case "successor queue":
				_, err = repo.db.ExecContext(ctx, repo.db.Rebind(`UPDATE task_sessions SET queue_incarnation_id = ? WHERE id = ?`), "successor-queue", sessionID)
				require.NoError(t, err)
			case "missing turn":
				_, err = repo.db.ExecContext(ctx, repo.db.Rebind(`UPDATE task_stop_request_candidates SET turn_id = '' WHERE task_id = ? AND operation_id = ?`), taskID, request.OperationID)
				require.NoError(t, err)
			}
			before, err := repo.GetTaskSession(ctx, sessionID)
			require.NoError(t, err)
			turn, err := repo.GetActiveTurnBySessionID(ctx, sessionID)
			require.NoError(t, err)
			changed, err := repo.FenceCoordinatorStopSessionForRequest(ctx, taskID, sessionID, request.OperationID)
			require.NoError(t, err)
			expectChanged := test == "matching" || test == "lifecycle sibling" || test == "preexisting lifecycle sibling"
			require.Equal(t, expectChanged, changed)
			if !expectChanged {
				after, readErr := repo.GetTaskSession(ctx, sessionID)
				require.NoError(t, readErr)
				require.Equal(t, before, after)
				afterTurn, readErr := repo.GetActiveTurnBySessionID(ctx, sessionID)
				require.NoError(t, readErr)
				require.Equal(t, turn, afterTurn)
				fences, readErr := repo.ListCoordinatorStopRequestSessionFences(ctx, taskID, request.OperationID)
				require.NoError(t, readErr)
				require.Empty(t, fences)
			} else {
				assertCoordinatorLaunchFenceClosedExactTurn(t, repo, turnID, test != "matching")
			}
		})
	}
}

func createCoordinatorLaunchLifecycleTurn(t *testing.T, repo *Repository, taskID, sessionID, turnID string, now time.Time) {
	t.Helper()
	require.NoError(t, repo.CreateTurn(context.Background(), &models.Turn{ID: "lifecycle-" + turnID, TaskID: taskID, TaskSessionID: sessionID, StartedAt: now.Add(time.Second), CreatedAt: now, UpdatedAt: now, Metadata: map[string]interface{}{models.TurnMetaKeyLifecycleOnly: true}}))
}

func assertCoordinatorLaunchFenceClosedExactTurn(t *testing.T, repo *Repository, turnID string, withSibling bool) {
	t.Helper()
	turn, err := repo.GetTurn(context.Background(), turnID)
	require.NoError(t, err)
	require.NotNil(t, turn.CompletedAt)
	if withSibling {
		sibling, err := repo.GetTurn(context.Background(), "lifecycle-"+turnID)
		require.NoError(t, err)
		require.Nil(t, sibling.CompletedAt)
	}
}

func assertCoordinatorStopPendingLaunchRegistration(t *testing.T, repo *Repository) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: "launch-task", Title: "launch"}))
	require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: "launch-parent", Title: "parent"}))
	require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{ID: "launch-session", TaskID: "launch-task", QueueIncarnationID: "captured-launch", State: models.TaskSessionStateStarting}))
	now := time.Now().UTC()
	require.NoError(t, repo.CreateTurn(ctx, &models.Turn{ID: "launch-turn", TaskID: "launch-task", TaskSessionID: "launch-session", StartedAt: now, CreatedAt: now, UpdatedAt: now}))
	request := models.CoordinatorStopRequest{TaskID: "launch-task", ParentTaskID: "launch-parent", OperationID: "pending-launch"}
	_, candidates, _, err := repo.CaptureCoordinatorStopRequestWithCandidates(ctx, request)
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	require.Empty(t, candidates[0].ExecutionID)
	require.Equal(t, "launch-turn", candidates[0].TurnID)
	running := &models.ExecutorRunning{ID: "launch-session", SessionID: "launch-session", TaskID: "launch-task", ExecutorID: "executor", Runtime: agentruntime.RuntimeStandalone, AgentExecutionID: "late-execution", AgentctlGeneration: 1, Status: models.ExecutorRunningStatusRunning}
	require.ErrorIs(t, repo.UpsertExecutorRunning(ctx, running), models.ErrExecutionStopFenced)
	_, err = repo.GetExecutorRunningBySessionID(ctx, "launch-session")
	require.ErrorIs(t, err, models.ErrExecutorRunningNotFound)
	session, err := repo.GetTaskSession(ctx, "launch-session")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateStarting, session.State)
	require.Equal(t, "captured-launch", session.QueueIncarnationID)
	_, err = repo.db.ExecContext(ctx, repo.db.Rebind(`UPDATE task_session_turns SET completed_at = ? WHERE id = ?`), now, "launch-turn")
	require.NoError(t, err)
	require.NoError(t, repo.CreateTurn(ctx, &models.Turn{ID: "successor-launch-turn", TaskID: "launch-task", TaskSessionID: "launch-session", StartedAt: now.Add(time.Second), CreatedAt: now, UpdatedAt: now}))
	running.AgentExecutionID, running.AgentctlGeneration = "same-queue-successor", 2
	require.NoError(t, repo.UpsertExecutorRunning(ctx, running), "a new authoritative turn on the same queue is independent of the captured launch")
	_, err = repo.db.ExecContext(ctx, repo.db.Rebind(`UPDATE task_sessions SET queue_incarnation_id = ? WHERE id = ?`), "successor-launch", session.ID)
	require.NoError(t, err)
	running.AgentExecutionID, running.AgentctlGeneration = "independent-successor", 3
	require.NoError(t, repo.UpsertExecutorRunning(ctx, running), "a different immutable launch incarnation remains admissible")
	_, retryCandidates, _, err := repo.CaptureCoordinatorStopRequestWithCandidates(ctx, request)
	require.NoError(t, err)
	require.Equal(t, candidates, retryCandidates)
}
