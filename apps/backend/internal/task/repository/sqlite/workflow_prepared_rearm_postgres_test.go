package sqlite

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/testutil"
	"github.com/stretchr/testify/require"
)

// Recovery must yield unchanged to either admission owner without holding the
// task row while waiting for an advisory lock used by turn/runtime writers.
func TestPreparedWorkflowRearmTurnLockOrderPostgres(t *testing.T) {
	for _, namespace := range []string{sessionTurnLockNamespace, agentDeliverySessionLockNamespace} {
		t.Run(namespace, func(t *testing.T) { testPreparedWorkflowRearmYieldsToAdmissionOwner(t, namespace) })
	}
}

func testPreparedWorkflowRearmYieldsToAdmissionOwner(t *testing.T, namespace string) {
	dsn := testutil.PostgresDSNFromEnv(t)
	database := openIsolatedPostgresMultiConn(t, dsn, 4)
	repo, err := NewWithDB(database, database, nil)
	require.NoError(t, err)
	taskID, route := seedPreparedWorkflowRearm(t, repo)
	writer := openWorkflowPostgresConnection(t, database, dsn)
	writerRepo := NewWithInitializedDB(writer, writer, nil)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	owner, err := database.BeginTxx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = owner.Rollback() }()
	_, err = owner.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, namespace+route.DestinationID)
	require.NoError(t, err)
	type result struct {
		changed bool
		err     error
	}
	outcome := make(chan result, 1)
	go func() {
		changed, err := writerRepo.RearmPreparedWorkflowSession(ctx, taskID, route)
		outcome <- result{changed, err}
	}()
	select {
	case result := <-outcome:
		require.NoError(t, result.err)
		require.False(t, result.changed)
	case <-time.After(time.Second):
		_ = owner.Rollback()
		<-outcome
		t.Fatal("rearm waited at an owned advisory boundary instead of yielding unchanged")
	}
	var lockedTask string
	require.NoError(t, owner.QueryRowContext(ctx, database.Rebind(`SELECT id FROM tasks WHERE id = ? FOR UPDATE NOWAIT`), taskID).Scan(&lockedTask))
	require.Equal(t, taskID, lockedTask)
	destination, err := repo.GetTaskSession(ctx, route.DestinationID)
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateFailed, destination.State)
	require.NoError(t, owner.Rollback())
	changed, err := writerRepo.RearmPreparedWorkflowSession(ctx, taskID, route)
	require.NoError(t, err)
	require.True(t, changed, "released admission ownership permits a later exact retry")
}

func TestPreparedWorkflowRearmRacesActualTurnAdmissionPostgres(t *testing.T) {
	dsn := testutil.PostgresDSNFromEnv(t)
	database := openIsolatedPostgresMultiConn(t, dsn, 4)
	repo, err := NewWithDB(database, database, nil)
	require.NoError(t, err)
	taskID, route := seedPreparedWorkflowRearm(t, repo)
	turnDB := openWorkflowPostgresConnection(t, database, dsn)
	turnRepo := NewWithInitializedDB(turnDB, turnDB, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	start := make(chan struct{})
	rearmed := make(chan error, 1)
	admitted := make(chan error, 1)
	go func() { <-start; _, err := repo.RearmPreparedWorkflowSession(ctx, taskID, route); rearmed <- err }()
	go func() {
		<-start
		turn := &models.Turn{ID: "concurrent-first-turn", TaskID: taskID, TaskSessionID: route.DestinationID}
		stamped, err := turnRepo.CreateTurnWithStepStamp(ctx, turn)
		if err == nil && !stamped {
			err = fmt.Errorf("turn admission fell back without its step stamp")
		}
		admitted <- err
	}()
	close(start)
	require.NoError(t, <-rearmed)
	require.NoError(t, <-admitted, "admission must not fall back to an unstamped turn after a deadlock")
	turns, err := repo.ListTurnsBySession(ctx, route.DestinationID)
	require.NoError(t, err)
	require.Len(t, turns, 1)
	changed, err := repo.RearmPreparedWorkflowSession(ctx, taskID, route)
	require.NoError(t, err)
	require.False(t, changed, "the committed first turn fences any later rearm")
}

func openWorkflowPostgresConnection(t *testing.T, database *sqlx.DB, dsn string) *sqlx.DB {
	t.Helper()
	var schema string
	require.NoError(t, database.Get(&schema, `SELECT current_schema()`))
	require.True(t, strings.HasPrefix(schema, "kandev_test_"))
	connection, err := sqlx.Open("pgx", testutil.PostgresDSNWithSearchPath(t, dsn, schema))
	require.NoError(t, err)
	connection.SetMaxOpenConns(1)
	connection.SetMaxIdleConns(1)
	t.Cleanup(func() { _ = connection.Close() })
	return connection
}

func TestPreparedWorkflowRearmRacesDeliveryAdmissionPostgres(t *testing.T) {
	dsn := testutil.PostgresDSNFromEnv(t)
	database := openIsolatedPostgresMultiConn(t, dsn, 4)
	repo, err := NewWithDB(database, database, nil)
	require.NoError(t, err)
	taskID, route := seedPreparedWorkflowRearm(t, repo)
	deliveryDB := openWorkflowPostgresConnection(t, database, dsn)
	deliveryRepo := NewWithInitializedDB(deliveryDB, deliveryDB, nil)
	destination, err := repo.GetTaskSession(t.Context(), route.DestinationID)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	start := make(chan struct{})
	outcomes := make(chan error, 2)
	go func() { <-start; _, err := repo.RearmPreparedWorkflowSession(ctx, taskID, route); outcomes <- err }()
	go func() {
		<-start
		_, err := deliveryRepo.PrepareAgentDeliverySubmission(ctx, &models.AgentDeliverySubmission{
			ID: "concurrent-submission", SessionID: route.DestinationID, IncarnationID: destination.QueueIncarnationID,
			PayloadHash: "hash", Payload: []byte("first prompt"),
		})
		outcomes <- err
	}()
	close(start)
	require.NoError(t, <-outcomes)
	require.NoError(t, <-outcomes)
	submission, err := repo.GetAgentDeliverySubmission(ctx, "concurrent-submission")
	require.NoError(t, err)
	require.Equal(t, route.DestinationID, submission.SessionID)
	changed, err := repo.RearmPreparedWorkflowSession(ctx, taskID, route)
	require.NoError(t, err)
	require.False(t, changed, "delivery ownership fences all subsequent rearm")
}

func TestPreparedWorkflowPromotionAndPendingPromptPostgres(t *testing.T) {
	dsn := testutil.PostgresDSNFromEnv(t)
	database := openIsolatedPostgresMultiConn(t, dsn, 4)
	repo, err := NewWithDB(database, database, nil)
	require.NoError(t, err)
	taskID, route := seedPreparedWorkflowRearm(t, repo)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	changed, err := repo.RearmPreparedWorkflowSession(ctx, taskID, route)
	require.NoError(t, err)
	require.True(t, changed)
	secondDB := openWorkflowPostgresConnection(t, database, dsn)
	secondRepo := NewWithInitializedDB(secondDB, secondDB, nil)
	committed := route
	committed.Phase = "committed"
	type result struct {
		promoted bool
		err      error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	for _, writer := range []*Repository{repo, secondRepo} {
		go func() {
			<-start
			promoted, err := writer.PromotePreparedWorkflowSession(ctx, route.DestinationID, committed)
			results <- result{promoted, err}
		}()
	}
	close(start)
	winners := 0
	for range 2 {
		outcome := <-results
		require.NoError(t, outcome.err)
		if outcome.promoted {
			winners++
		}
	}
	require.Equal(t, 1, winners)
	task, err := repo.GetTask(ctx, taskID)
	require.NoError(t, err)
	pending, ok := models.LoadWorkflowSessionRoute(task.Metadata)
	require.True(t, ok)
	require.True(t, pending.FirstPromptPending)
	valid, err := repo.ValidatePendingWorkflowFirstPrompt(ctx, taskID, pending)
	require.NoError(t, err)
	require.True(t, valid)
	stamped, err := secondRepo.CreateTurnWithStepStamp(ctx, &models.Turn{ID: "promoted-first-turn", TaskID: taskID, TaskSessionID: route.DestinationID})
	require.NoError(t, err)
	require.True(t, stamped)
	valid, err = repo.ValidatePendingWorkflowFirstPrompt(ctx, taskID, pending)
	require.NoError(t, err)
	require.False(t, valid)
	require.NoError(t, repo.CompletePendingWorkflowFirstPrompt(ctx, taskID, pending))
	task, err = repo.GetTask(ctx, taskID)
	require.NoError(t, err)
	finished, ok := models.LoadWorkflowSessionRoute(task.Metadata)
	require.True(t, ok)
	require.False(t, finished.FirstPromptPending)
}

func TestPreparedWorkflowRearmRuntimeLockOrderPostgres(t *testing.T) {
	dsn := testutil.PostgresDSNFromEnv(t)
	database := openIsolatedPostgresMultiConn(t, dsn, 4)
	repo, err := NewWithDB(database, database, nil)
	require.NoError(t, err)
	taskID, route := seedPreparedWorkflowRearm(t, repo)
	writer := openWorkflowPostgresConnection(t, database, dsn)
	writerRepo := NewWithInitializedDB(writer, writer, nil)
	var pid int
	require.NoError(t, writer.Get(&pid, `SELECT pg_backend_pid()`))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	runtimeOwner, err := database.BeginTxx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = runtimeOwner.Rollback() }()
	// Exact first lock used by managedSessionStateWriter and CreateExecutorRunning.
	require.NoError(t, repo.managedSessionDeletionBarrierTx(ctx, runtimeOwner, route.DestinationID))
	outcome := make(chan error, 1)
	go func() { _, err := writerRepo.RearmPreparedWorkflowSession(ctx, taskID, route); outcome <- err }()
	require.Eventually(t, func() bool {
		var waiting bool
		return database.GetContext(ctx, &waiting, `SELECT wait_event_type = 'Lock' FROM pg_stat_activity WHERE pid = $1`, pid) == nil && waiting
	}, 3*time.Second, 10*time.Millisecond)
	var turnAvailable bool
	require.NoError(t, runtimeOwner.QueryRowContext(ctx,
		`SELECT pg_try_advisory_xact_lock(hashtextextended($1, 0))`, sessionTurnLockNamespace+route.DestinationID).Scan(&turnAvailable))
	require.NoError(t, runtimeOwner.Rollback())
	require.NoError(t, <-outcome)
	require.True(t, turnAvailable, "runtime admission owns task row; rearm must not own turn lock while waiting on that row")
}

func TestPreparedWorkflowRearmRacesRuntimeAdmissionPostgres(t *testing.T) {
	for _, admission := range []string{"state", "executor"} {
		t.Run(admission, func(t *testing.T) { testPreparedWorkflowRuntimeAdmission(t, admission) })
	}
}

func testPreparedWorkflowRuntimeAdmission(t *testing.T, admission string) {
	dsn := testutil.PostgresDSNFromEnv(t)
	database := openIsolatedPostgresMultiConn(t, dsn, 4)
	repo, err := NewWithDB(database, database, nil)
	require.NoError(t, err)
	taskID, route := seedPreparedWorkflowRearm(t, repo)
	runtimeDB := openWorkflowPostgresConnection(t, database, dsn)
	runtimeRepo := NewWithInitializedDB(runtimeDB, runtimeDB, nil)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	start := make(chan struct{})
	outcomes := make(chan error, 2)
	go func() { <-start; _, err := repo.RearmPreparedWorkflowSession(ctx, taskID, route); outcomes <- err }()
	go func() {
		<-start
		var err error
		if admission == "state" {
			err = runtimeRepo.UpdateTaskSessionState(ctx, route.DestinationID, models.TaskSessionStateStarting, "")
		} else {
			err = runtimeRepo.UpsertExecutorRunning(ctx, &models.ExecutorRunning{
				TaskID: taskID, SessionID: route.DestinationID, ResumeToken: "native-owner", Status: models.ExecutorRunningStatusRunning,
			})
		}
		outcomes <- err
	}()
	close(start)
	require.NoError(t, <-outcomes, "runtime and retry must serialize without deadlock aborts")
	require.NoError(t, <-outcomes)
	changed, err := repo.RearmPreparedWorkflowSession(ctx, taskID, route)
	require.NoError(t, err)
	require.False(t, changed, "persisted runtime/state ownership fences a subsequent retry")
	if admission == "state" {
		destination, err := repo.GetTaskSession(ctx, route.DestinationID)
		require.NoError(t, err)
		require.Equal(t, models.TaskSessionStateStarting, destination.State)
	} else {
		var token string
		require.NoError(t, database.GetContext(ctx, &token, database.Rebind(`SELECT resume_token FROM executors_running WHERE session_id = ?`), route.DestinationID))
		require.Equal(t, "native-owner", token)
	}
}
