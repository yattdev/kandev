package orchestrator

import (
	"github.com/kandev/kandev/internal/events/bus"
	"github.com/kandev/kandev/internal/task/repository/sqlite"
	"github.com/kandev/kandev/internal/testutil"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

func TestCoordinatorStopServiceStartResumesOriginalRequest(t *testing.T) {
	assertCoordinatorStopServiceStart(t, setupTestRepo(t))
}

func TestPostgresCoordinatorStopServiceStartResumesOriginalRequest(t *testing.T) {
	db := testutil.OpenIsolatedPostgres(t, testutil.PostgresDSNFromEnv(t))
	repo, err := sqlite.NewWithDB(db, db, nil)
	require.NoError(t, err)
	assertCoordinatorStopServiceStart(t, repo)
}

func assertCoordinatorStopServiceStart(t *testing.T, repo *sqlite.Repository) {
	repo, svc, stops := newLegacyStopFixtureWithRepo(t, repo)
	svc = NewService(DefaultServiceConfig(), bus.NewMemoryEventBus(testLogger()), svc.agentManager, svc.taskRepo, repo, nil, nil, nil, testLogger())
	ctx := t.Context()
	request := models.CoordinatorStopRequest{TaskID: "legacy-task", ParentTaskID: "legacy-parent", OperationID: "service-start-stop"}
	_, candidates, _, err := repo.CaptureCoordinatorStopRequestWithCandidates(ctx, request)
	require.NoError(t, err)
	svc.SetTurnService(&repoTurnService{repo: repo})
	require.NoError(t, svc.Start(ctx))
	t.Cleanup(func() { require.NoError(t, svc.Stop()) })
	result, err := svc.GetCoordinatorStopReceipt(ctx, request.TaskID, request.ParentTaskID, request.OperationID)
	require.NoError(t, err)
	require.Equal(t, CoordinatorTaskStopStatusStopped, result.Status)
	require.EqualValues(t, 1, stops.Load())
	_, saved, _, err := repo.CaptureCoordinatorStopRequestWithCandidates(ctx, request)
	require.NoError(t, err)
	require.Equal(t, candidates, saved)
	require.EqualValues(t, 1, stops.Load())
	replayed, err := svc.StopTaskForCoordinatorOperation(ctx, request.TaskID, request.ParentTaskID, request.OperationID)
	require.NoError(t, err)
	require.Equal(t, CoordinatorTaskStopStatusStopped, replayed.Status)
	require.EqualValues(t, 1, stops.Load())
}

func TestCoordinatorStopPeriodicResumesOriginalAfterStartup(t *testing.T) {
	assertCoordinatorStopPeriodic(t, setupTestRepo(t))
}

func TestPostgresCoordinatorStopPeriodicResumesOriginalAfterStartup(t *testing.T) {
	db := testutil.OpenIsolatedPostgres(t, testutil.PostgresDSNFromEnv(t))
	repo, err := sqlite.NewWithDB(db, db, nil)
	require.NoError(t, err)
	assertCoordinatorStopPeriodic(t, repo)
}

func assertCoordinatorStopPeriodic(t *testing.T, repo *sqlite.Repository) {
	repo, svc, stops := newLegacyStopFixtureWithRepo(t, repo)
	svc = NewService(DefaultServiceConfig(), bus.NewMemoryEventBus(testLogger()), svc.agentManager, svc.taskRepo, repo, nil, nil, nil, testLogger())
	ctx := t.Context()
	request := models.CoordinatorStopRequest{TaskID: "legacy-task", ParentTaskID: "legacy-parent", OperationID: "periodic-stop"}
	ticks := make(chan time.Time, 1)
	svc.coordinatorStopReconciler.ticks = ticks
	_, candidates, _, err := repo.CaptureCoordinatorStopRequestWithCandidates(ctx, request)
	require.NoError(t, err)
	running, err := repo.GetExecutorRunningBySessionID(ctx, "legacy-session")
	require.NoError(t, err)
	require.NoError(t, repo.DeleteExecutorRunningBySessionID(ctx, "legacy-session"))
	svc.SetTurnService(&repoTurnService{repo: repo})
	require.NoError(t, svc.Start(ctx))
	t.Cleanup(func() { require.NoError(t, svc.Stop()) })
	first, err := svc.GetCoordinatorStopReceipt(ctx, request.TaskID, request.ParentTaskID, request.OperationID)
	require.NoError(t, err)
	require.Equal(t, CoordinatorTaskStopStatusIncomplete, first.Status)
	require.NoError(t, repo.UpsertExecutorRunning(ctx, running))
	ticks <- time.Now()
	require.Eventually(t, func() bool {
		result, err := svc.GetCoordinatorStopReceipt(ctx, request.TaskID, request.ParentTaskID, request.OperationID)
		return err == nil && result.Status == CoordinatorTaskStopStatusStopped
	}, 3*time.Second, time.Millisecond, "the service-owned periodic cycle must complete the original pending request")
	result, err := svc.GetCoordinatorStopReceipt(ctx, request.TaskID, request.ParentTaskID, request.OperationID)
	require.NoError(t, err)
	require.Equal(t, CoordinatorTaskStopStatusStopped, result.Status)
	_, saved, _, err := repo.CaptureCoordinatorStopRequestWithCandidates(ctx, request)
	require.NoError(t, err)
	require.Equal(t, candidates, saved)
	require.EqualValues(t, 1, stops.Load())
	replayed, err := svc.StopTaskForCoordinatorOperation(ctx, request.TaskID, request.ParentTaskID, request.OperationID)
	require.NoError(t, err)
	require.Equal(t, CoordinatorTaskStopStatusStopped, replayed.Status)
	require.EqualValues(t, 1, stops.Load())
}
