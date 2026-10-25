package orchestrator

import (
	"context"
	"github.com/jmoiron/sqlx"
	internaldb "github.com/kandev/kandev/internal/db"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/events/bus"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository/sqlite"
	"github.com/kandev/kandev/internal/testutil"
	"github.com/stretchr/testify/require"
)

type coordinatorGuardObservedRepo struct {
	*sqlite.Repository
	observe atomic.Bool
	entered chan struct{}
}

func (r *coordinatorGuardObservedRepo) ListCoordinatorStopRequestSessionFences(ctx context.Context, taskID, operationID string) ([]models.CoordinatorStopSessionFenceReceipt, error) {
	fences, err := r.Repository.ListCoordinatorStopRequestSessionFences(ctx, taskID, operationID)
	if r.observe.Load() {
		select {
		case r.entered <- struct{}{}:
		default:
		}
	}
	return fences, err
}

// @covers AC-STOP-FENCE-005, AC-STOP-FENCE-008
func TestCoordinatorStopPeriodicSessionGuardCancellation(t *testing.T) {
	assertCoordinatorStopSessionGuard(t, setupTestRepo(t))
}
func TestPostgresCoordinatorStopPeriodicSessionGuardCancellation(t *testing.T) {
	db := openCoordinatorGuardPostgres(t)
	repo, err := sqlite.NewWithDB(db, db, nil)
	require.NoError(t, err)
	assertCoordinatorStopSessionGuard(t, repo)
}

func assertCoordinatorStopSessionGuard(t *testing.T, repo *sqlite.Repository) {
	ctx := t.Context()
	_, prior, stops := newLegacyStopFixtureWithRepo(t, repo)
	observed := &coordinatorGuardObservedRepo{Repository: repo, entered: make(chan struct{}, 1)}
	svc := NewService(DefaultServiceConfig(), bus.NewMemoryEventBus(testLogger()), prior.agentManager, prior.taskRepo, observed, nil, nil, nil, testLogger())
	svc.SetTurnService(&repoTurnService{repo: repo})
	request := models.CoordinatorStopRequest{TaskID: "legacy-task", ParentTaskID: "legacy-parent", OperationID: "session-guard-stop"}
	_, captured, _, err := repo.CaptureCoordinatorStopRequestWithCandidates(ctx, request)
	require.NoError(t, err)
	row, err := repo.GetExecutorRunningBySessionID(ctx, "legacy-session")
	require.NoError(t, err)
	require.NoError(t, repo.DeleteExecutorRunningBySessionID(ctx, row.SessionID))
	ticks := make(chan time.Time, 1)
	svc.coordinatorStopReconciler.ticks = ticks
	require.NoError(t, svc.Start(ctx))
	require.NoError(t, repo.UpsertExecutorRunning(ctx, row))
	row, err = repo.GetExecutorRunningBySessionID(ctx, row.SessionID)
	require.NoError(t, err)
	guard, releaseRef := svc.acquireCancelInFlightGuard(row.SessionID)
	guard.Lock()
	endOwner := svc.beginCancelInFlight(row.SessionID)
	_, ownerRevision := svc.CancellationPendingSnapshot(row.SessionID)
	held := true
	releaseOwner := func() {
		if held {
			held = false
			guard.Unlock()
			releaseRef()
			endOwner()
		}
	}
	defer releaseOwner()
	observed.observe.Store(true)
	ticks <- time.Now()
	select {
	case <-observed.entered:
	case <-time.After(3 * time.Second):
		releaseOwner()
		require.NoError(t, svc.Stop())
		t.Fatal("worker did not reach captured session admission")
	}
	done := make(chan error, 1)
	go func() { done <- svc.Stop() }()
	bounded := false
	select {
	case err := <-done:
		require.NoError(t, err)
		bounded = true
	case <-time.After(time.Second):
		releaseOwner()
		require.NoError(t, <-done)
	}
	require.True(t, bounded, "Service.Stop must join without releasing another session cancellation owner")
	require.True(t, svc.isCancelInFlight(row.SessionID))
	_, retainedRevision := svc.CancellationPendingSnapshot(row.SessionID)
	require.Equal(t, ownerRevision, retainedRevision)
	svc.cancelInFlightMu.Lock()
	refs := svc.cancelInFlight[row.SessionID].refs
	svc.cancelInFlightMu.Unlock()
	require.Equal(t, 1, refs, "refused admission must release only its own guard reference")
	require.Zero(t, stops.Load())
	unchanged, err := repo.GetExecutorRunningBySessionID(ctx, row.SessionID)
	require.NoError(t, err)
	require.Equal(t, row, unchanged)
	_, saved, _, err := repo.CaptureCoordinatorStopRequestWithCandidates(ctx, request)
	require.NoError(t, err)
	require.Equal(t, captured, saved)
	releaseOwner()
	for range 2 {
		result, err := svc.StopTaskForCoordinatorOperation(ctx, request.TaskID, request.ParentTaskID, request.OperationID)
		require.NoError(t, err)
		require.Equal(t, CoordinatorTaskStopStatusStopped, result.Status)
	}
	require.EqualValues(t, 1, stops.Load())
	svc.cancelInFlightMu.Lock()
	require.Empty(t, svc.cancelInFlight)
	svc.cancelInFlightMu.Unlock()
	require.False(t, svc.isCancelInFlight(row.SessionID))
}

func TestCoordinatorStopSessionGuardCallerCancellationAndDeadline(t *testing.T) {
	for _, mode := range []string{"cancel", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			repo, svc, stops := newLegacyStopFixture(t)
			observed := &coordinatorGuardObservedRepo{Repository: repo, entered: make(chan struct{}, 1)}
			observed.observe.Store(true)
			svc.repo = observed
			guard, releaseRef := svc.acquireCancelInFlightGuard("legacy-session")
			guard.Lock()
			held := true
			release := func() {
				if held {
					held = false
					guard.Unlock()
					releaseRef()
				}
			}
			defer release()
			ctx, cancel := context.WithCancel(t.Context())
			if mode == "deadline" {
				cancel()
				ctx, cancel = context.WithTimeout(t.Context(), 300*time.Millisecond)
			}
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, err := svc.StopTaskForCoordinatorOperation(ctx, "legacy-task", "legacy-parent", "caller-guard-stop")
				done <- err
			}()
			select {
			case <-observed.entered:
			case <-time.After(time.Second):
				release()
				<-done
				t.Fatal("caller did not reach captured session admission")
			}
			if mode == "cancel" {
				cancel()
			}
			bounded := false
			select {
			case err := <-done:
				require.Error(t, err)
				bounded = true
			case <-time.After(time.Second):
				release()
				<-done
			}
			require.True(t, bounded, "caller lifetime must bound admission while another guard remains held")
			require.Zero(t, stops.Load())
			request, candidates, _, err := repo.CaptureCoordinatorStopRequestWithCandidates(t.Context(), models.CoordinatorStopRequest{TaskID: "legacy-task", ParentTaskID: "legacy-parent", OperationID: "caller-guard-stop"})
			require.NoError(t, err)
			require.False(t, request.Complete)
			require.Len(t, candidates, 1)
			require.Equal(t, "legacy-execution", candidates[0].ExecutionID)
			require.EqualValues(t, 1, candidates[0].AgentctlGeneration)
			release()
			result, err := svc.StopTaskForCoordinatorOperation(t.Context(), request.TaskID, request.ParentTaskID, request.OperationID)
			require.NoError(t, err)
			require.Equal(t, CoordinatorTaskStopStatusStopped, result.Status)
			require.EqualValues(t, 1, stops.Load())
		})
	}
}

// Cancellation may replace a database connection, so each new connection must retain the owned schema.
func openCoordinatorGuardPostgres(t *testing.T) *sqlx.DB {
	t.Helper()
	dsn := testutil.PostgresDSNFromEnv(t)
	setup := testutil.OpenIsolatedPostgres(t, dsn)
	var schema string
	require.NoError(t, setup.QueryRow("SELECT current_schema()").Scan(&schema))
	scopedDSN := dsn + " options='-c search_path=" + schema + "'"
	if strings.Contains(dsn, "://") {
		separator := "?"
		if strings.Contains(dsn, "?") {
			separator = "&"
		}
		scopedDSN = dsn + separator + "options=" + url.QueryEscape("-c search_path="+schema)
	}
	raw, err := internaldb.OpenPostgres(scopedDSN, 1, 1)
	require.NoError(t, err)
	pool := sqlx.NewDb(raw, "pgx")
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	return pool
}
