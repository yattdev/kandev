package messagequeue

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCensusClaimTransactionSQLiteWriterCommitsBeforeClaimedRemoval(t *testing.T) {
	ctx := context.Background()
	repo := newTestSQLiteRepo(t).(*sqliteRepository)
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
	started := make(chan struct{})
	removed := make(chan error, 1)
	go func() {
		close(started)
		_, removeErr := repo.DeleteClaimedPending(ctx, identity, "execution-1", "entry-a", before.Entries[0].ClaimSHA256)
		removed <- removeErr
	}()
	<-started
	require.NoError(t, tx.Commit())
	require.ErrorIs(t, <-removed, ErrQueueChanged)
	rows, err := repo.ListBySession(ctx, identity.SessionID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "after", rows[0].Content)
	after, err := repo.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
	require.NoError(t, err)
	_, err = repo.DeleteClaimedPending(ctx, identity, "execution-1", "entry-a", after.Entries[0].ClaimSHA256)
	require.NoError(t, err)
}
