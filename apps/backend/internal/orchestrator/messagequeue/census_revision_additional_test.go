package messagequeue

import (
	"context"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCensusRevisionPeriodicReplacementOverflowRollsBack(t *testing.T) {
	for _, factory := range autoRunRepositoryFactories {
		t.Run(factory.name, func(t *testing.T) {
			ctx := context.Background()
			repo := factory.new(t)
			identity := managedInputIdentity("periodic-overflow-" + factory.name)
			seedQueueSessionIdentity(t, repo, identity)
			seedCensusExecution(t, repo, identity)
			storage := requireManagedInputStorage(t, repo)
			old, replay, err := storage.AdmitManagedInput(ctx, identity,
				managedInputRequest("periodic-1", "occurrence-1", "old", ManagedInputOriginPeriodic, "status"), 1)
			require.NoError(t, err)
			require.False(t, replay)
			switch typed := repo.(type) {
			case *memoryRepository:
				typed.mu.Lock()
				typed.censusRevision[identity.SessionID] = math.MaxInt64
				typed.mu.Unlock()
			case *sqliteRepository:
				_, err := typed.db.ExecContext(ctx, typed.db.Rebind(`UPDATE queue_session_locks SET census_revision = ? WHERE session_id = ?`),
					int64(math.MaxInt64), identity.SessionID)
				require.NoError(t, err)
			}
			guarded := repo.(censusTransactionRepository)
			before, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", 1)
			require.NoError(t, err)
			_, replay, err = storage.AdmitManagedInput(ctx, identity,
				managedInputRequest("periodic-2", "occurrence-2", "new", ManagedInputOriginPeriodic, "status"), 1)
			require.ErrorIs(t, err, errCensusRevisionExhausted)
			require.False(t, replay)
			after, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", 1)
			require.NoError(t, err)
			require.Equal(t, before, after)
			receipt, err := storage.GetManagedInput(ctx, identity, old.ID)
			require.NoError(t, err)
			require.Equal(t, ManagedInputStateAccepted, receipt.State)
			_, err = storage.GetManagedInput(ctx, identity, "periodic-2")
			require.ErrorIs(t, err, ErrEntryNotFound)
			rows, err := repo.ListBySession(ctx, identity.SessionID)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.Equal(t, old.ID, rows[0].ID)
			require.Equal(t, "old", rows[0].Content)
		})
	}
}

func TestCensusRevisionLegacyPendingMoveMigrationBumpsOnce(t *testing.T) {
	ctx := context.Background()
	repo := newTestSQLiteRepo(t).(*sqliteRepository)
	identity := QueueSessionIdentity{TaskID: "task-migrate-move", SessionID: "session-migrate-move", SessionIncarnationID: "incarnation-migrate-move"}
	seedQueueSessionIdentity(t, repo, identity)
	seedCensusExecution(t, repo, identity)
	_, err := repo.db.ExecContext(ctx, repo.db.Rebind(`INSERT INTO pending_moves (id, move_id, session_id, task_id, queued_at) VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)`),
		"row-migrate", "move-migrate", identity.SessionID, identity.TaskID)
	require.NoError(t, err)
	before, err := repo.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
	require.NoError(t, err)
	require.NoError(t, repo.migratePendingMoveIdentities())
	after, err := repo.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
	require.NoError(t, err)
	require.Greater(t, after.Revision, before.Revision)
	require.NoError(t, repo.migratePendingMoveIdentities())
	replayed, err := repo.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
	require.NoError(t, err)
	require.Equal(t, after.Revision, replayed.Revision)
}

func TestCensusRevisionGenericDeleteOverflowRetainsClaim(t *testing.T) {
	for _, factory := range autoRunRepositoryFactories {
		t.Run(factory.name, func(t *testing.T) {
			ctx := context.Background()
			repo := factory.new(t)
			identity := QueueSessionIdentity{TaskID: "task-delete-overflow", SessionID: "session-delete-overflow", SessionIncarnationID: "incarnation-delete-overflow"}
			seedQueueSessionIdentity(t, repo, identity)
			seedCensusExecution(t, repo, identity)
			require.NoError(t, repo.InsertForSession(ctx, identity, &QueuedMessage{
				ID: "entry-a", TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "A", QueuedBy: QueuedByUser,
			}, DefaultMaxPerSession))
			switch typed := repo.(type) {
			case *memoryRepository:
				typed.mu.Lock()
				typed.censusRevision[identity.SessionID] = math.MaxInt64
				typed.mu.Unlock()
			case *sqliteRepository:
				_, err := typed.db.ExecContext(ctx, typed.db.Rebind(`UPDATE queue_session_locks SET census_revision = ? WHERE session_id = ?`),
					int64(math.MaxInt64), identity.SessionID)
				require.NoError(t, err)
			}
			guarded := repo.(censusTransactionRepository)
			before, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			_, err = repo.DeleteByIDForSession(ctx, identity, "entry-a")
			require.ErrorIs(t, err, errCensusRevisionExhausted)
			after, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			require.Equal(t, before, after)
			rows, err := repo.ListBySession(ctx, identity.SessionID)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.Equal(t, "A", rows[0].Content)
		})
	}
}

func TestCensusRevisionMemoryAuthorityReplacementRetainsTombstone(t *testing.T) {
	ctx := context.Background()
	identity := QueueSessionIdentity{TaskID: "task-authority-rotation", SessionID: "session-authority-rotation", SessionIncarnationID: "incarnation-1"}
	current := identity
	repo := NewMemoryRepositoryWithAuthority(func(context.Context, string, string) (QueueSessionIdentity, error) {
		return current, nil
	}).(*memoryRepository)
	resolved, err := repo.ResolveSessionIdentity(ctx, identity.TaskID, identity.SessionID)
	require.NoError(t, err)
	require.Equal(t, identity, resolved)
	seedCensusExecution(t, repo, identity)
	require.NoError(t, repo.InsertForSession(ctx, identity, &QueuedMessage{
		ID: "entry-a", TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "A", QueuedBy: QueuedByUser,
	}, DefaultMaxPerSession))
	before, err := repo.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
	require.NoError(t, err)
	current.SessionIncarnationID = "incarnation-2"
	resolved, err = repo.ResolveSessionIdentity(ctx, current.TaskID, current.SessionID)
	require.NoError(t, err)
	require.Equal(t, current, resolved)
	seedCensusExecution(t, repo, current)
	after, err := repo.ObservePendingCensus(ctx, current, "execution-1", DefaultMaxPerSession)
	require.NoError(t, err)
	require.Empty(t, after.Entries)
	require.Greater(t, after.Revision, before.Revision)
}

func TestCensusRevisionUnchangedEditIsNoop(t *testing.T) {
	for _, factory := range autoRunRepositoryFactories {
		t.Run(factory.name, func(t *testing.T) {
			ctx := context.Background()
			repo := factory.new(t)
			identity := QueueSessionIdentity{TaskID: "task-edit-noop", SessionID: "session-edit-noop", SessionIncarnationID: "incarnation-edit-noop"}
			seedQueueSessionIdentity(t, repo, identity)
			seedCensusExecution(t, repo, identity)
			require.NoError(t, repo.InsertForSession(ctx, identity, &QueuedMessage{
				ID: "entry-a", TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "same", QueuedBy: QueuedByUser,
			}, DefaultMaxPerSession))
			guarded := repo.(censusTransactionRepository)
			before, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			require.NoError(t, repo.UpdateContentAndMetadataForSession(ctx, identity, "entry-a", "same", nil, nil, QueuedByUser))
			after, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			require.Equal(t, before, after)
		})
	}
}

func TestCensusRevisionUnprojectedEditWriters(t *testing.T) {
	for _, factory := range autoRunRepositoryFactories {
		for _, edit := range []struct {
			name        string
			attachments []MessageAttachment
			metadata    map[string]interface{}
		}{
			{name: "attachments", attachments: []MessageAttachment{{AttachmentID: "attachment-1"}}},
			{name: "metadata", metadata: map[string]interface{}{"private-note": "changed"}},
		} {
			t.Run(factory.name+"/"+edit.name, func(t *testing.T) {
				ctx := context.Background()
				repo := factory.new(t)
				identity := QueueSessionIdentity{TaskID: "task-unprojected-edit", SessionID: "session-unprojected-edit", SessionIncarnationID: "incarnation-unprojected-edit"}
				seedQueueSessionIdentity(t, repo, identity)
				seedCensusExecution(t, repo, identity)
				require.NoError(t, repo.InsertForSession(ctx, identity, &QueuedMessage{
					ID: "entry-a", TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "same", QueuedBy: QueuedByUser,
				}, DefaultMaxPerSession))
				guarded := repo.(censusTransactionRepository)
				before, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
				require.NoError(t, err)
				require.NoError(t, repo.UpdateContentAndMetadataForSession(ctx, identity, "entry-a", "same", edit.attachments, edit.metadata, QueuedByUser))
				after, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
				require.NoError(t, err)
				require.Equal(t, before.Entries[0].ContentHash, after.Entries[0].ContentHash)
				require.Greater(t, after.Revision, before.Revision)
				_, err = guarded.DeleteClaimedPending(ctx, identity, "execution-1", "entry-a", before.Entries[0].ClaimSHA256)
				require.ErrorIs(t, err, ErrQueueChanged)
			})
		}
	}
}
