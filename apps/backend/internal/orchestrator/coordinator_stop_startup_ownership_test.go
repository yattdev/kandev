package orchestrator

import (
	"context"
	"errors"
	"testing"

	agentRuntime "github.com/kandev/kandev/internal/agent/runtime"
	"github.com/kandev/kandev/internal/events/bus"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository/sqlite"
	"github.com/kandev/kandev/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestCoordinatorStopStartupRetainsUnverifiedRuntime(t *testing.T) {
	assertCoordinatorStopStartupOwnership(t, setupTestRepo(t))
}

func TestPostgresCoordinatorStopStartupRetainsUnverifiedRuntime(t *testing.T) {
	db := testutil.OpenIsolatedPostgres(t, testutil.PostgresDSNFromEnv(t))
	repo, err := sqlite.NewWithDB(db, db, nil)
	require.NoError(t, err)
	assertCoordinatorStopStartupOwnership(t, repo)
}

func assertCoordinatorStopStartupOwnership(t *testing.T, repo *sqlite.Repository) {
	ctx := t.Context()
	_, prior, stops := newLegacyStopFixtureWithRepo(t, repo)
	request := models.CoordinatorStopRequest{TaskID: "legacy-task", ParentTaskID: "legacy-parent", OperationID: "owned-stop-restart"}
	_, candidates, _, err := repo.CaptureCoordinatorStopRequestWithCandidates(ctx, request)
	require.NoError(t, err)
	row, err := repo.GetExecutorRunningBySessionID(ctx, "legacy-session")
	require.NoError(t, err)
	_, _, err = repo.CaptureCoordinatorStopOperation(ctx, models.CoordinatorStopOperation{ID: "owned-stop-intent", RequestOperationID: request.OperationID, TaskID: request.TaskID, SessionID: row.SessionID, TurnID: "legacy-turn", ExecutionID: row.AgentExecutionID, AgentctlGeneration: row.AgentctlGeneration, ExecutorStatus: row.Status, ExecutorUpdatedAt: row.UpdatedAt})
	require.NoError(t, err)
	manager := prior.agentManager.(*mockAgentManager)
	manager.closeExecutionAdmissionFunc = func(context.Context, string, uint64) (*agentRuntime.ExecutionFenceReceipt, error) {
		return nil, errors.New("retained incarnation unknown")
	}
	svc := NewService(DefaultServiceConfig(), bus.NewMemoryEventBus(testLogger()), manager, prior.taskRepo, repo, nil, nil, nil, testLogger())
	svc.SetTurnService(&repoTurnService{repo: repo})
	require.NoError(t, svc.Start(ctx))
	t.Cleanup(func() { require.NoError(t, svc.Stop()) })
	result, err := svc.GetCoordinatorStopReceipt(ctx, request.TaskID, request.ParentTaskID, request.OperationID)
	require.NoError(t, err)
	require.Equal(t, CoordinatorTaskStopStatusIncomplete, result.Status)
	require.Zero(t, stops.Load(), "ordinary startup cleanup must not bypass exact stop ownership")
	savedRow, err := repo.GetExecutorRunningBySessionID(ctx, row.SessionID)
	require.NoError(t, err)
	require.Equal(t, row, savedRow)
	_, saved, _, err := repo.CaptureCoordinatorStopRequestWithCandidates(ctx, request)
	require.NoError(t, err)
	require.Equal(t, candidates, saved)
}
