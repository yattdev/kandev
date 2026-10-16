package sqlite

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/orchestrator/messagequeue"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceOwnerTransferRejectsOldEntryClaim(t *testing.T) {
	ctx := context.Background()
	repo, task := seedTaskTransferFixture(t)
	queue, err := messagequeue.NewSQLiteRepository(repo.db, repo.ro)
	require.NoError(t, err)
	_, err = repo.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS users (id TEXT PRIMARY KEY, status TEXT NOT NULL);
		INSERT INTO users (id, status) VALUES ('owner-1', 'active'), ('owner-2', 'active')`)
	require.NoError(t, err)
	session, err := repo.GetTaskSession(ctx, "session-running")
	require.NoError(t, err)
	identity := messagequeue.QueueSessionIdentity{TaskID: task.ID, SessionID: session.ID, SessionIncarnationID: session.QueueIncarnationID}
	ownerCtx := messagequeue.WithCensusOwnerFence(messagequeue.WithCensusWorkspaceFence(ctx, task.WorkspaceID), "owner-1")
	now := time.Now().UTC()
	_, err = repo.db.ExecContext(ctx, repo.db.Rebind(`INSERT INTO executors_running
		(id, session_id, task_id, executor_id, agent_execution_id, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 'running', ?, ?)`), "running-1", session.ID, task.ID, "executor-1", "execution-1", now, now)
	require.NoError(t, err)
	require.NoError(t, queue.InsertForSession(ctx, identity, &messagequeue.QueuedMessage{
		ID: "entry-1", SessionID: session.ID, TaskID: task.ID, Content: "keep", QueuedBy: messagequeue.QueuedByUser,
	}, messagequeue.DefaultMaxPerSession))
	claimed := queue.(interface {
		ObservePendingCensus(context.Context, messagequeue.QueueSessionIdentity, string, int) (messagequeue.PendingCensus, error)
		DeleteClaimedPending(context.Context, messagequeue.QueueSessionIdentity, string, string, string) (*messagequeue.QueueRemovalResult, error)
	})
	before, err := claimed.ObservePendingCensus(ownerCtx, identity, "execution-1", 0)
	require.NoError(t, err)
	require.Len(t, before.Entries, 1)
	require.NoError(t, repo.TransferWorkspaceOwnership(ctx, task.WorkspaceID, "owner-1", "owner-2"))
	require.NoError(t, repo.TransferWorkspaceOwnership(ctx, task.WorkspaceID, "owner-2", "owner-1"))
	_, err = claimed.DeleteClaimedPending(ownerCtx, identity, "execution-1", "entry-1", before.Entries[0].ClaimSHA256)
	require.ErrorIs(t, err, messagequeue.ErrQueueChanged)
	after, err := claimed.ObservePendingCensus(ownerCtx, identity, "execution-1", 0)
	require.NoError(t, err)
	require.Len(t, after.Entries, 1)
	require.Equal(t, "entry-1", after.Entries[0].ID)
	_, err = repo.db.ExecContext(ctx, repo.db.Rebind(`UPDATE workspaces SET owner_id = ? WHERE id = ?`), "owner-2", task.WorkspaceID)
	require.NoError(t, err)
	_, err = claimed.DeleteClaimedPending(ownerCtx, identity, "execution-1", "entry-1", after.Entries[0].ClaimSHA256)
	require.ErrorIs(t, err, messagequeue.ErrSessionIdentityMismatch)
	_, err = repo.db.ExecContext(ctx, repo.db.Rebind(`UPDATE workspaces SET owner_id = ? WHERE id = ?`), "owner-1", task.WorkspaceID)
	require.NoError(t, err)
	_, err = repo.db.ExecContext(ctx, `UPDATE users SET status = 'disabled' WHERE id = 'owner-1'`)
	require.NoError(t, err)
	_, err = claimed.DeleteClaimedPending(ownerCtx, identity, "execution-1", "entry-1", after.Entries[0].ClaimSHA256)
	require.ErrorIs(t, err, messagequeue.ErrSessionIdentityMismatch)
	_, err = repo.db.ExecContext(ctx, `UPDATE users SET status = 'active' WHERE id = 'owner-1'`)
	require.NoError(t, err)
	removed, err := claimed.DeleteClaimedPending(ownerCtx, identity, "execution-1", "entry-1", after.Entries[0].ClaimSHA256)
	require.NoError(t, err)
	require.Len(t, removed.Removed, 1)
	_, err = claimed.DeleteClaimedPending(ownerCtx, identity, "execution-1", "entry-1", after.Entries[0].ClaimSHA256)
	require.Error(t, err)
	require.NoError(t, repo.TransferWorkspaceOwnership(ctx, task.WorkspaceID, "owner-1", "owner-2"))
	workspace, err := repo.GetWorkspace(ctx, task.WorkspaceID)
	require.NoError(t, err)
	require.Equal(t, "owner-2", workspace.OwnerID)
}

func TestWorkspaceOwnerRotationInvalidatesQueueCensus(t *testing.T) {
	ctx := context.Background()
	repo, task := seedTaskTransferFixture(t)
	_, err := messagequeue.NewSQLiteRepository(repo.db, repo.ro)
	require.NoError(t, err)
	require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{ID: "session-other", TaskID: task.ID, State: models.TaskSessionStateCreated}))
	ids := []string{"session-other", "session-running"}
	for _, id := range ids {
		_, err = repo.db.ExecContext(ctx, repo.db.Rebind(`INSERT INTO queue_session_locks (session_id) VALUES (?)`), id)
		require.NoError(t, err)
	}
	for _, owner := range []struct{ from, to string }{{"owner-1", "owner-2"}, {"owner-2", "owner-1"}} {
		require.NoError(t, repo.TransferWorkspaceOwnership(ctx, task.WorkspaceID, owner.from, owner.to))
	}
	for _, id := range ids {
		var revision int64
		require.NoError(t, repo.db.GetContext(ctx, &revision, repo.db.Rebind(`SELECT census_revision FROM queue_session_locks WHERE session_id = ?`), id))
		require.Equal(t, int64(2), revision)
	}
	require.NoError(t, repo.TransferWorkspaceOwnership(ctx, task.WorkspaceID, "owner-1", "owner-1"))
	for _, id := range ids {
		var revision int64
		require.NoError(t, repo.db.GetContext(ctx, &revision, repo.db.Rebind(`SELECT census_revision FROM queue_session_locks WHERE session_id = ?`), id))
		require.Equal(t, int64(2), revision)
	}
	_, err = repo.db.ExecContext(ctx, repo.db.Rebind(`UPDATE queue_session_locks SET census_revision = ? WHERE session_id = ?`), int64(math.MaxInt64), ids[1])
	require.NoError(t, err)
	require.Error(t, repo.TransferWorkspaceOwnership(ctx, task.WorkspaceID, "owner-1", "owner-2"))
	workspace, err := repo.GetWorkspace(ctx, task.WorkspaceID)
	require.NoError(t, err)
	require.Equal(t, "owner-1", workspace.OwnerID)
	var firstRevision int64
	require.NoError(t, repo.db.GetContext(ctx, &firstRevision, repo.db.Rebind(`SELECT census_revision FROM queue_session_locks WHERE session_id = ?`), ids[0]))
	require.Equal(t, int64(2), firstRevision)
}
