package messagequeue

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

func TestCensusClaimTransactionSQLiteWriterCommitsBeforeClaimedRemoval(t *testing.T) {
	ctx := context.Background()
	raw, err := sql.Open("sqlite3", "file:"+filepath.Join(t.TempDir(), "queue.db")+"?_busy_timeout=0&_journal_mode=WAL")
	require.NoError(t, err)
	raw.SetMaxOpenConns(2)
	raw.SetMaxIdleConns(2)
	db := sqlx.NewDb(raw, "sqlite3")
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`CREATE TABLE task_sessions (id TEXT PRIMARY KEY)`)
	require.NoError(t, err)
	created, err := NewSQLiteRepository(db, db)
	require.NoError(t, err)
	repo := created.(*sqliteRepository)
	identity := QueueSessionIdentity{
		TaskID: "task-writer-barrier", SessionID: "session-writer-barrier", SessionIncarnationID: "incarnation-writer-barrier",
	}
	seedQueueSessionIdentity(t, repo, identity)
	seedCensusExecution(t, repo, identity)
	require.NoError(t, repo.InsertForSession(ctx, identity, &QueuedMessage{
		ID: "entry-a", TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "before", QueuedBy: QueuedByUser,
	}, DefaultMaxPerSession))
	before, err := repo.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
	require.NoError(t, err)

	// Hold the same durable session lock used by an editing writer while a
	// claimed removal enters through another pooled connection.
	tx, err := repo.beginSessionMutationTx(ctx, identity.SessionID, "writer barrier")
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, repo.db.Rebind(`UPDATE queued_messages SET content = ? WHERE id = ? AND session_id = ?`),
		"after", "entry-a", identity.SessionID)
	require.NoError(t, err)
	require.NoError(t, bumpCensusRevisionTx(ctx, tx, repo.db, identity.SessionID))
	// A second pooled connection must reach SQLite's write lock while the
	// writer transaction is still open. A zero busy timeout makes that
	// boundary observable as SQLITE_BUSY without relying on scheduling.
	_, err = repo.DeleteClaimedPending(ctx, identity, "execution-1", "entry-a", before.Entries[0].ClaimSHA256)
	var sqliteErr sqlite3.Error
	require.True(t, errors.As(err, &sqliteErr), "claimed removal must encounter the held write lock: %v", err)
	require.Equal(t, sqlite3.ErrBusy, sqliteErr.Code)
	require.NoError(t, tx.Commit())
	_, err = repo.DeleteClaimedPending(ctx, identity, "execution-1", "entry-a", before.Entries[0].ClaimSHA256)
	require.ErrorIs(t, err, ErrQueueChanged)
	rows, err := repo.ListBySession(ctx, identity.SessionID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "after", rows[0].Content)
	after, err := repo.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
	require.NoError(t, err)
	_, err = repo.DeleteClaimedPending(ctx, identity, "execution-1", "entry-a", after.Entries[0].ClaimSHA256)
	require.NoError(t, err)
}
