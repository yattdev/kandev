package orchestrator

import (
	"context"
	"errors"
	"testing"
	"time"

	agentRuntime "github.com/kandev/kandev/internal/agent/runtime"
	"github.com/kandev/kandev/internal/events/bus"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository/sqlite"
	"github.com/kandev/kandev/internal/testutil"
	"github.com/stretchr/testify/require"
)

// @covers AC-STOP-FENCE-005, AC-STOP-FENCE-008
func TestCoordinatorStopPeriodicShutdownCancelsProductionReplay(t *testing.T) {
	assertCoordinatorStopPeriodicCancellation(t, setupTestRepo(t))
}

func TestPostgresCoordinatorStopPeriodicShutdownCancelsProductionReplay(t *testing.T) {
	db := testutil.OpenIsolatedPostgres(t, testutil.PostgresDSNFromEnv(t))
	repo, err := sqlite.NewWithDB(db, db, nil)
	require.NoError(t, err)
	assertCoordinatorStopPeriodicCancellation(t, repo)
}

func assertCoordinatorStopPeriodicCancellation(t *testing.T, repo *sqlite.Repository) {
	ctx := t.Context()
	_, prior, stops := newLegacyStopFixtureWithRepo(t, repo)
	svc := NewService(DefaultServiceConfig(), bus.NewMemoryEventBus(testLogger()), prior.agentManager, prior.taskRepo, repo, nil, nil, nil, testLogger())
	svc.SetTurnService(&repoTurnService{repo: repo})
	request := models.CoordinatorStopRequest{TaskID: "legacy-task", ParentTaskID: "legacy-parent", OperationID: "canceled-periodic"}
	_, candidates, _, err := repo.CaptureCoordinatorStopRequestWithCandidates(ctx, request)
	require.NoError(t, err)
	row, err := repo.GetExecutorRunningBySessionID(ctx, "legacy-session")
	require.NoError(t, err)
	require.NoError(t, repo.DeleteExecutorRunningBySessionID(ctx, row.SessionID))
	ticks := make(chan time.Time, 1)
	svc.coordinatorStopReconciler.ticks = ticks
	require.NoError(t, svc.Start(ctx))
	entered, rescue := make(chan struct{}), make(chan struct{})
	producerContext := make(chan context.Context, 1)
	manager := prior.agentManager.(*mockAgentManager)
	manager.closeExecutionAdmissionFunc = func(ctx context.Context, _ string, _ uint64) (*agentRuntime.ExecutionFenceReceipt, error) {
		producerContext <- ctx
		close(entered)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-rescue:
			return nil, errors.New("test-owned rescue")
		}
	}
	require.NoError(t, repo.UpsertExecutorRunning(ctx, row))
	ticks <- time.Now()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		require.NoError(t, svc.Stop())
		t.Fatal("periodic production replay did not enter exact producer")
	}
	runtimeCtx := <-producerContext
	_, boundedDeadline := runtimeCtx.Deadline()
	done := make(chan error, 1)
	go func() { done <- svc.Stop() }()
	joined := false
	select {
	case err := <-done:
		require.NoError(t, err)
		joined = true
	case <-time.After(time.Second):
		close(rescue)
		require.NoError(t, <-done)
	}
	require.True(t, boundedDeadline, "actual exact producer must retain the worker deadline")
	require.True(t, joined, "Service.Stop must cancel and join actual pending replay without releasing runtime drain")
	require.ErrorIs(t, runtimeCtx.Err(), context.Canceled)
	require.Zero(t, stops.Load())
	operations, err := repo.ListCoordinatorStopOperations(ctx, request.TaskID)
	require.NoError(t, err)
	require.Len(t, operations, 1)
	require.NotEqual(t, models.CoordinatorStopOperationStatusStopped, operations[0].Status)
	require.Equal(t, models.CoordinatorStopProofScopePending, operations[0].ProofScope)
	_, saved, _, err := repo.CaptureCoordinatorStopRequestWithCandidates(ctx, request)
	require.NoError(t, err)
	require.Equal(t, candidates, saved)
}
