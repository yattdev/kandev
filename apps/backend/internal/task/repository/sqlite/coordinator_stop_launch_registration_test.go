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
	_, err = repo.db.ExecContext(ctx, repo.db.Rebind(`UPDATE task_sessions SET queue_incarnation_id = ? WHERE id = ?`), "successor-launch", session.ID)
	require.NoError(t, err)
	running.AgentExecutionID, running.AgentctlGeneration = "independent-successor", 2
	require.NoError(t, repo.UpsertExecutorRunning(ctx, running), "a different immutable launch incarnation remains admissible")
	_, retryCandidates, _, err := repo.CaptureCoordinatorStopRequestWithCandidates(ctx, request)
	require.NoError(t, err)
	require.Equal(t, candidates, retryCandidates)
}
