package messagequeue

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/common/logger"
	"github.com/stretchr/testify/require"
)

// @covers AC-TASKS-ACTIVE-QUEUE-CENSUS-001.6 AC-TASKS-ACTIVE-QUEUE-CENSUS-001.7
func TestActiveQueueCensusSQLiteReadOnly(t *testing.T) {
	testActiveQueueCensusSQLReadOnly(t, newTestSQLiteRepo(t))
}

// @covers AC-TASKS-ACTIVE-QUEUE-CENSUS-001.7
func TestActiveQueueCensusPostgres(t *testing.T) {
	testActiveQueueCensusSQLReadOnly(t, newTestPostgresRepo(t))
}

func testActiveQueueCensusSQLReadOnly(t *testing.T, repo Repository) {
	ctx := context.Background()
	identity := QueueSessionIdentity{TaskID: "task", SessionID: "session", SessionIncarnationID: "incarnation"}
	seedQueueSessionIdentity(t, repo, identity)
	db := repo.(*sqliteRepository).db
	_, err := db.Exec(`ALTER TABLE tasks ADD COLUMN workspace_id TEXT NOT NULL DEFAULT ''`)
	require.NoError(t, err)
	_, err = db.Exec(`ALTER TABLE tasks ADD COLUMN state TEXT NOT NULL DEFAULT 'IN_PROGRESS'`)
	require.NoError(t, err)
	_, err = db.Exec(`ALTER TABLE task_sessions ADD COLUMN state TEXT NOT NULL DEFAULT 'CREATED'`)
	require.NoError(t, err)
	_, err = db.Exec(`CREATE TABLE executors_running (
		session_id TEXT PRIMARY KEY, task_id TEXT NOT NULL,
		agent_execution_id TEXT NOT NULL, status TEXT NOT NULL)`)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE tasks SET workspace_id = 'workspace' WHERE id = 'task'`)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE task_sessions SET state = 'RUNNING' WHERE id = 'session'`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO executors_running VALUES ('session', 'task', 'execution', 'running')`)
	require.NoError(t, err)
	service := NewService(repo, DefaultMaxPerSession, logger.Default())
	proof := ActiveQueueCensusProof{Identity: identity, ExecutionID: "execution", WorkspaceID: "workspace"}
	empty, err := service.GetActiveQueueCensus(ctx, proof)
	require.NoError(t, err)
	require.Equal(t, 0, empty.Count)
	require.NotNil(t, empty.Entries)
	require.Empty(t, empty.Entries)
	require.NoError(t, repo.InsertForSession(ctx, identity, &QueuedMessage{
		ID: "hidden", TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "not pending",
		Metadata: map[string]interface{}{MetadataLifecycleReserved: true},
	}, DefaultMaxPerSession))
	require.NoError(t, repo.InsertForSession(ctx, identity, &QueuedMessage{
		ID: "one", TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "first",
	}, DefaultMaxPerSession))
	var beforeGeneration int64
	require.NoError(t, db.Get(&beforeGeneration, `SELECT status_generation FROM queue_session_state WHERE session_id = 'session'`))
	result, err := service.GetActiveQueueCensus(ctx, proof)
	require.NoError(t, err)
	require.Equal(t, 1, result.Count)
	require.Equal(t, "one", result.Entries[0].ID)
	var afterGeneration int64
	require.NoError(t, db.Get(&afterGeneration, `SELECT status_generation FROM queue_session_state WHERE session_id = 'session'`))
	require.Equal(t, beforeGeneration, afterGeneration)

	proof.ExecutionID = "stale-execution"
	denied, err := service.GetActiveQueueCensus(ctx, proof)
	require.ErrorIs(t, err, ErrSessionIdentityMismatch)
	require.Nil(t, denied)
	proof.ExecutionID = "execution"
	_, err = db.Exec(`UPDATE tasks SET state = 'COMPLETED' WHERE id = 'task'`)
	require.NoError(t, err)
	denied, err = service.GetActiveQueueCensus(ctx, proof)
	require.ErrorIs(t, err, ErrSessionIdentityMismatch)
	require.Nil(t, denied)
}
