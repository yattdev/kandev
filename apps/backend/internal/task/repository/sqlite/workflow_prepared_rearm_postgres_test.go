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

// The held turn lock is the first boundary shared by actual turn creation and
// reconstruction. Rearm must not hold the task row while waiting at it.
func TestPreparedWorkflowRearmTurnLockOrderPostgres(t *testing.T) {
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
	turnOwner, err := database.BeginTxx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = turnOwner.Rollback() }()
	require.NoError(t, lockSessionTurnWrites(ctx, turnOwner, database.DriverName(), route.DestinationID))
	outcome := make(chan error, 1)
	go func() { _, err := writerRepo.RearmPreparedWorkflowSession(ctx, taskID, route); outcome <- err }()
	require.Eventually(t, func() bool {
		var waiting bool
		if err := database.GetContext(ctx, &waiting, `SELECT wait_event_type = 'Lock' FROM pg_stat_activity WHERE pid = $1`, pid); err != nil {
			return false
		}
		return waiting
	}, 3*time.Second, 10*time.Millisecond, "actual rearm must reach the held turn boundary")
	var lockedTask string
	err = turnOwner.QueryRowContext(ctx, database.Rebind(`SELECT id FROM tasks WHERE id = ? FOR UPDATE NOWAIT`), taskID).Scan(&lockedTask)
	// On the old task -> turn implementation this errors with SQLSTATE 55P03,
	// proving the opposing turn -> task writer would close a deadlock cycle.
	if err != nil {
		_ = turnOwner.Rollback()
		<-outcome
		require.NoError(t, err, "rearm must not own the task row before the turn lock")
		return
	}
	require.Equal(t, taskID, lockedTask)
	require.NoError(t, turnOwner.Rollback())
	require.NoError(t, <-outcome)
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
