package sqlite

import (
	"context"
	"math"
	"testing"

	"github.com/kandev/kandev/internal/orchestrator/messagequeue"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

func TestTransferTaskAdvancesQueueCensusForEverySession(t *testing.T) {
	ctx := context.Background()
	repo, task := seedTaskTransferFixture(t)
	_, err := messagequeue.NewSQLiteRepository(repo.db, repo.ro)
	require.NoError(t, err)
	var sessionIDs []string
	require.NoError(t, repo.db.SelectContext(ctx, &sessionIDs, repo.db.Rebind(`SELECT id FROM task_sessions WHERE task_id = ? ORDER BY id`), task.ID))
	require.NotEmpty(t, sessionIDs)
	for _, sessionID := range sessionIDs {
		_, err = repo.db.ExecContext(ctx, repo.db.Rebind(`INSERT INTO queue_session_locks (session_id) VALUES (?) ON CONFLICT(session_id) DO NOTHING`), sessionID)
		require.NoError(t, err)
	}
	command := taskTransferCommand(task)
	_, err = repo.TransferTask(ctx, command)
	require.NoError(t, err)
	revisions := make(map[string]int64, len(sessionIDs))
	for _, sessionID := range sessionIDs {
		var revision int64
		require.NoError(t, repo.db.GetContext(ctx, &revision, repo.db.Rebind(`SELECT census_revision FROM queue_session_locks WHERE session_id = ?`), sessionID))
		require.Positive(t, revision, sessionID)
		revisions[sessionID] = revision
	}
	_, err = repo.TransferTask(ctx, command)
	require.NoError(t, err)
	for _, sessionID := range sessionIDs {
		var revision int64
		require.NoError(t, repo.db.GetContext(ctx, &revision, repo.db.Rebind(`SELECT census_revision FROM queue_session_locks WHERE session_id = ?`), sessionID))
		require.Equal(t, revisions[sessionID], revision, sessionID)
	}
}

func TestTransferTaskCensusOverflowRollsBackPlacement(t *testing.T) {
	ctx := context.Background()
	repo, task := seedTaskTransferFixture(t)
	_, err := messagequeue.NewSQLiteRepository(repo.db, repo.ro)
	require.NoError(t, err)
	require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{
		ID: "session-aux", TaskID: task.ID, State: models.TaskSessionStateCreated,
	}))
	var sessionIDs []string
	require.NoError(t, repo.db.SelectContext(ctx, &sessionIDs,
		repo.db.Rebind(`SELECT id FROM task_sessions WHERE task_id = ? ORDER BY id`), task.ID))
	require.Len(t, sessionIDs, 2)
	for _, sessionID := range sessionIDs {
		_, err = repo.db.ExecContext(ctx, repo.db.Rebind(`INSERT INTO queue_session_locks (session_id) VALUES (?)
			ON CONFLICT(session_id) DO NOTHING`), sessionID)
		require.NoError(t, err)
	}
	_, err = repo.db.ExecContext(ctx, repo.db.Rebind(`UPDATE queue_session_locks SET census_revision = ? WHERE session_id = ?`),
		int64(math.MaxInt64), sessionIDs[len(sessionIDs)-1])
	require.NoError(t, err)

	_, err = repo.TransferTask(ctx, taskTransferCommand(task))
	require.Error(t, err)
	var workspaceID string
	require.NoError(t, repo.db.GetContext(ctx, &workspaceID,
		repo.db.Rebind(`SELECT workspace_id FROM tasks WHERE id = ?`), task.ID))
	require.Equal(t, task.WorkspaceID, workspaceID)
	for index, sessionID := range sessionIDs {
		var revision int64
		require.NoError(t, repo.db.GetContext(ctx, &revision,
			repo.db.Rebind(`SELECT census_revision FROM queue_session_locks WHERE session_id = ?`), sessionID))
		if index == len(sessionIDs)-1 {
			require.Equal(t, int64(math.MaxInt64), revision)
		} else {
			require.Zero(t, revision)
		}
	}
}
