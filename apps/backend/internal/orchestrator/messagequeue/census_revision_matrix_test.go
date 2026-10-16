package messagequeue

import (
	"context"
	"math"
	"testing"

	"github.com/kandev/kandev/internal/common/logger"
	"github.com/stretchr/testify/require"
)

func TestCensusRevisionDestructiveAndMoveWriters(t *testing.T) {
	actions := []struct {
		name string
		act  func(context.Context, Repository, QueueSessionIdentity) error
	}{
		{name: "delete-all", act: func(ctx context.Context, repo Repository, identity QueueSessionIdentity) error {
			_, err := repo.DeleteAllBySessionForIdentity(ctx, identity)
			return err
		}},
		{name: "pending-move", act: func(ctx context.Context, repo Repository, identity QueueSessionIdentity) error {
			return repo.SetPendingMove(ctx, identity.SessionID, &PendingMove{
				TaskID: identity.TaskID, SessionIncarnationID: identity.SessionIncarnationID,
			})
		}},
	}
	for _, factory := range autoRunRepositoryFactories {
		for _, action := range actions {
			t.Run(factory.name+"/"+action.name, func(t *testing.T) {
				ctx := context.Background()
				repo := factory.new(t)
				identity := QueueSessionIdentity{TaskID: "task-matrix", SessionID: "session-matrix", SessionIncarnationID: "incarnation-matrix"}
				seedQueueSessionIdentity(t, repo, identity)
				seedCensusExecution(t, repo, identity)
				message := &QueuedMessage{ID: "entry-a", TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "A", QueuedBy: QueuedByUser}
				require.NoError(t, repo.InsertForSession(ctx, identity, message, DefaultMaxPerSession))
				guarded := repo.(censusTransactionRepository)
				before, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
				require.NoError(t, err)
				require.NoError(t, action.act(ctx, repo, identity))
				after, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
				require.NoError(t, err)
				require.Greater(t, after.Revision, before.Revision)
				_, err = guarded.DeleteClaimedPending(ctx, identity, "execution-1", message.ID, before.Entries[0].ClaimSHA256)
				require.ErrorIs(t, err, ErrQueueChanged)
			})
		}
	}
}

func TestCensusRevisionMemoryCompoundAdmissionOverflowRollsBack(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryRepository().(*memoryRepository)
	identity := QueueSessionIdentity{TaskID: "task-overflow", SessionID: "session-overflow", SessionIncarnationID: "incarnation-overflow"}
	seedQueueSessionIdentity(t, repo, identity)
	seedCensusExecution(t, repo, identity)
	require.NoError(t, repo.InsertForSession(ctx, identity, &QueuedMessage{
		ID: "entry-a", TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "A", QueuedBy: QueuedByUser,
	}, DefaultMaxPerSession))
	repo.mu.Lock()
	repo.censusRevision[identity.SessionID] = math.MaxInt64 - 1
	repo.mu.Unlock()
	_, _, err := repo.AdmitQueueMessage(ctx, identity, "entry-b", &QueuedMessage{
		TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "B", QueuedBy: QueuedByUser,
	}, nil, DefaultMaxPerSession, &AutoMergePolicy{Enabled: true, Source: AutoMergeSourceGlobal}, nil)
	require.ErrorIs(t, err, errCensusRevisionExhausted)
	rows, err := repo.ListBySession(ctx, identity.SessionID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "entry-a", rows[0].ID)
	repo.mu.Lock()
	revision := repo.censusRevision[identity.SessionID]
	repo.mu.Unlock()
	require.Equal(t, int64(math.MaxInt64-1), revision)
}

func TestCensusRevisionPolicyReplayDoesNotAdvance(t *testing.T) {
	for _, factory := range autoRunRepositoryFactories {
		t.Run(factory.name, func(t *testing.T) {
			ctx := context.Background()
			repo := factory.new(t)
			identity := QueueSessionIdentity{TaskID: "task-policy-replay", SessionID: "session-policy-replay", SessionIncarnationID: "incarnation-policy-replay"}
			seedQueueSessionIdentity(t, repo, identity)
			seedCensusExecution(t, repo, identity)
			require.NoError(t, repo.SetAutoRunForSession(ctx, identity, false))
			guarded := repo.(censusTransactionRepository)
			before, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			require.NoError(t, repo.SetAutoRunForSession(ctx, identity, false))
			after, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			require.Equal(t, before.Revision, after.Revision)
		})
	}
}

func TestCensusRevisionEditLeaseInvalidatesClaim(t *testing.T) {
	for _, factory := range autoRunRepositoryFactories {
		t.Run(factory.name, func(t *testing.T) {
			ctx := context.Background()
			repo := factory.new(t)
			identity := QueueSessionIdentity{TaskID: "task-lease", SessionID: "session-lease", SessionIncarnationID: "incarnation-lease"}
			seedQueueSessionIdentity(t, repo, identity)
			seedCensusExecution(t, repo, identity)
			require.NoError(t, repo.InsertForSession(ctx, identity, &QueuedMessage{
				ID: "entry-a", TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "A", QueuedBy: QueuedByUser,
			}, DefaultMaxPerSession))
			guarded := repo.(censusTransactionRepository)
			before, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			service := NewService(repo, DefaultMaxPerSession, logger.Default())
			lease, err := service.BeginEdit(ctx, identity.SessionID, "entry-a", "connection-1")
			require.NoError(t, err)
			afterBegin, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			require.Greater(t, afterBegin.Revision, before.Revision)
			_, err = guarded.DeleteClaimedPending(ctx, identity, "execution-1", "entry-a", afterBegin.Entries[0].ClaimSHA256)
			require.ErrorIs(t, err, ErrEditConflict)
			require.NoError(t, service.EndEdit(ctx, identity.SessionID, "entry-a", lease.LeaseID, "connection-1"))
			afterEnd, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			require.Greater(t, afterEnd.Revision, afterBegin.Revision)
		})
	}
}

func TestCensusRevisionLegacySnapshotIsPresentationOnly(t *testing.T) {
	for _, factory := range autoRunRepositoryFactories {
		t.Run(factory.name, func(t *testing.T) {
			ctx := context.Background()
			repo := factory.new(t)
			identity := QueueSessionIdentity{TaskID: "task-snapshot", SessionID: "session-snapshot", SessionIncarnationID: "incarnation-snapshot"}
			seedQueueSessionIdentity(t, repo, identity)
			seedCensusExecution(t, repo, identity)
			require.NoError(t, repo.InsertForSession(ctx, identity, &QueuedMessage{
				ID: "entry-a", TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "A", QueuedBy: QueuedByUser,
			}, DefaultMaxPerSession))
			guarded := repo.(censusTransactionRepository)
			before, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			_, err = repo.Snapshot(ctx, identity)
			require.NoError(t, err)
			after, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			require.Equal(t, before.Revision, after.Revision)
		})
	}
}

func TestCensusRevisionPurgeRetainsTombstone(t *testing.T) {
	for _, factory := range autoRunRepositoryFactories {
		t.Run(factory.name, func(t *testing.T) {
			ctx := context.Background()
			repo := factory.new(t)
			identity := QueueSessionIdentity{TaskID: "task-purge", SessionID: "session-purge", SessionIncarnationID: "incarnation-purge"}
			seedQueueSessionIdentity(t, repo, identity)
			seedCensusExecution(t, repo, identity)
			message := &QueuedMessage{ID: "entry-a", TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "A", QueuedBy: QueuedByUser}
			require.NoError(t, repo.InsertForSession(ctx, identity, message, DefaultMaxPerSession))
			guarded := repo.(censusTransactionRepository)
			before, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			removed, err := repo.PurgeSession(ctx, identity.SessionID)
			require.NoError(t, err)
			require.Equal(t, 1, removed)
			after, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			require.Empty(t, after.Entries)
			require.Greater(t, after.Revision, before.Revision)
			message.Position = 0
			require.NoError(t, repo.InsertForSession(ctx, identity, message, DefaultMaxPerSession))
			restored, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			require.Len(t, restored.Entries, 1)
			_, err = guarded.DeleteClaimedPending(ctx, identity, "execution-1", message.ID, before.Entries[0].ClaimSHA256)
			require.ErrorIs(t, err, ErrQueueChanged)
		})
	}
}

func TestCensusRevisionReplacementInvalidatesIdenticalRows(t *testing.T) {
	for _, factory := range autoRunRepositoryFactories {
		t.Run(factory.name, func(t *testing.T) {
			ctx := context.Background()
			repo := factory.new(t)
			identity := QueueSessionIdentity{TaskID: "task-replace", SessionID: "session-replace", SessionIncarnationID: "incarnation-replace"}
			seedQueueSessionIdentity(t, repo, identity)
			seedCensusExecution(t, repo, identity)
			message := &QueuedMessage{ID: "entry-a", TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "A", QueuedBy: QueuedByUser}
			require.NoError(t, repo.InsertForSession(ctx, identity, message, DefaultMaxPerSession))
			guarded := repo.(censusTransactionRepository)
			before, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			stored, err := repo.ListBySession(ctx, identity.SessionID)
			require.NoError(t, err)
			require.NoError(t, repo.ReplaceSessionForIdentity(ctx, identity, stored, nil))
			after, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			require.Equal(t, before.Entries[0].ID, after.Entries[0].ID)
			require.Equal(t, before.Entries[0].ContentHash, after.Entries[0].ContentHash)
			require.Greater(t, after.Revision, before.Revision)
			_, err = guarded.DeleteClaimedPending(ctx, identity, "execution-1", message.ID, before.Entries[0].ClaimSHA256)
			require.ErrorIs(t, err, ErrQueueChanged)
		})
	}
}

func TestCensusRevisionTransferAdvancesBothSessions(t *testing.T) {
	for _, factory := range autoRunRepositoryFactories {
		t.Run(factory.name, func(t *testing.T) {
			ctx := context.Background()
			repo := factory.new(t)
			source := QueueSessionIdentity{TaskID: "task-transfer", SessionID: "session-source", SessionIncarnationID: "incarnation-source"}
			destination := QueueSessionIdentity{TaskID: source.TaskID, SessionID: "session-destination", SessionIncarnationID: "incarnation-destination"}
			seedQueueSessionIdentity(t, repo, source)
			seedQueueSessionIdentity(t, repo, destination)
			seedCensusExecution(t, repo, source)
			seedCensusExecution(t, repo, destination)
			require.NoError(t, repo.InsertForSession(ctx, source, &QueuedMessage{
				ID: "entry-a", TaskID: source.TaskID, SessionID: source.SessionID, Content: "A", QueuedBy: QueuedByUser,
			}, DefaultMaxPerSession))
			guarded := repo.(censusTransactionRepository)
			beforeSource, err := guarded.ObservePendingCensus(ctx, source, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			beforeDestination, err := guarded.ObservePendingCensus(ctx, destination, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			require.NoError(t, repo.TransferSessionIdentities(ctx, source, destination))
			afterDestination, err := guarded.ObservePendingCensus(ctx, destination, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			require.Len(t, afterDestination.Entries, 1)
			switch typed := repo.(type) {
			case *memoryRepository:
				typed.mu.Lock()
				sourceRevision := typed.censusRevision[source.SessionID]
				typed.mu.Unlock()
				require.Greater(t, sourceRevision, beforeSource.Revision)
			case *sqliteRepository:
				afterSource, readErr := guarded.ObservePendingCensus(ctx, source, "execution-1", DefaultMaxPerSession)
				require.NoError(t, readErr)
				require.Empty(t, afterSource.Entries)
				require.Greater(t, afterSource.Revision, beforeSource.Revision)
			}
			require.Greater(t, afterDestination.Revision, beforeDestination.Revision)
		})
	}
}

func TestCensusRevisionTransferCompensationReplayIsNoop(t *testing.T) {
	ctx := context.Background()
	repo := newTestSQLiteRepo(t).(*sqliteRepository)
	source := QueueSessionIdentity{TaskID: "task-compensation", SessionID: "session-compensation-source", SessionIncarnationID: "incarnation-compensation-source"}
	destination := QueueSessionIdentity{TaskID: source.TaskID, SessionID: "session-compensation-destination", SessionIncarnationID: "incarnation-compensation-destination"}
	seedQueueSessionIdentity(t, repo, source)
	seedQueueSessionIdentity(t, repo, destination)
	compensation := SessionTransferCompensation{
		OperationID: "operation-compensation", TaskID: source.TaskID,
		FromSessionID: source.SessionID, ToSessionID: destination.SessionID,
		EntryIDs: []string{"entry-a"},
	}
	revision := func(sessionID string) int64 {
		var value int64
		require.NoError(t, repo.db.GetContext(ctx, &value, repo.db.Rebind(`SELECT census_revision FROM queue_session_locks WHERE session_id = ?`), sessionID))
		return value
	}
	require.NoError(t, repo.UpsertSessionTransferCompensation(ctx, compensation))
	firstSource, firstDestination := revision(source.SessionID), revision(destination.SessionID)
	require.Positive(t, firstSource)
	require.Positive(t, firstDestination)
	require.NoError(t, repo.UpsertSessionTransferCompensation(ctx, compensation))
	require.Equal(t, firstSource, revision(source.SessionID))
	require.Equal(t, firstDestination, revision(destination.SessionID))
	require.NoError(t, repo.DeleteSessionTransferCompensation(ctx, compensation.OperationID, compensation.TaskID, compensation.FromSessionID, compensation.ToSessionID))
	require.Greater(t, revision(source.SessionID), firstSource)
	require.Greater(t, revision(destination.SessionID), firstDestination)
}

func TestCensusRevisionTaskPurgeInvalidatesClaims(t *testing.T) {
	for _, factory := range autoRunRepositoryFactories {
		t.Run(factory.name, func(t *testing.T) {
			ctx := context.Background()
			repo := factory.new(t)
			identity := QueueSessionIdentity{TaskID: "task-task-purge", SessionID: "session-task-purge", SessionIncarnationID: "incarnation-task-purge"}
			seedQueueSessionIdentity(t, repo, identity)
			seedCensusExecution(t, repo, identity)
			require.NoError(t, repo.InsertForSession(ctx, identity, &QueuedMessage{
				ID: "entry-a", TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "A", QueuedBy: QueuedByUser,
			}, DefaultMaxPerSession))
			guarded := repo.(censusTransactionRepository)
			before, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			removed, err := repo.PurgeTask(ctx, identity.TaskID)
			require.NoError(t, err)
			require.Equal(t, 1, removed)
			after, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			require.Empty(t, after.Entries)
			require.Greater(t, after.Revision, before.Revision)
		})
	}
}

func TestCensusRevisionRestoreAndRequeueWriters(t *testing.T) {
	actions := []struct {
		name string
		act  func(context.Context, Repository, QueueSessionIdentity) error
	}{
		{name: "restore", act: func(ctx context.Context, repo Repository, identity QueueSessionIdentity) error {
			return repo.RestoreForSession(ctx, identity, &QueuedMessage{
				ID: "entry-b", TaskID: identity.TaskID, SessionID: identity.SessionID, Position: 2, Content: "B", QueuedBy: QueuedByUser,
			}, DefaultMaxPerSession)
		}},
		{name: "requeue", act: func(ctx context.Context, repo Repository, identity QueueSessionIdentity) error {
			return repo.RequeuePreservingFIFOForSession(ctx, identity, &QueuedMessage{
				ID: "entry-b", TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "B", QueuedBy: QueuedByUser,
			})
		}},
	}
	for _, factory := range autoRunRepositoryFactories {
		for _, action := range actions {
			t.Run(factory.name+"/"+action.name, func(t *testing.T) {
				ctx := context.Background()
				repo := factory.new(t)
				identity := QueueSessionIdentity{TaskID: "task-restore", SessionID: "session-restore", SessionIncarnationID: "incarnation-restore"}
				seedQueueSessionIdentity(t, repo, identity)
				seedCensusExecution(t, repo, identity)
				require.NoError(t, repo.InsertForSession(ctx, identity, &QueuedMessage{
					ID: "entry-a", TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "A", QueuedBy: QueuedByUser,
				}, DefaultMaxPerSession))
				guarded := repo.(censusTransactionRepository)
				before, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
				require.NoError(t, err)
				require.NoError(t, action.act(ctx, repo, identity))
				after, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
				require.NoError(t, err)
				require.Len(t, after.Entries, 2)
				require.Greater(t, after.Revision, before.Revision)
			})
		}
	}
}

func TestCensusRevisionCoalescedReplacementWriter(t *testing.T) {
	for _, factory := range autoRunRepositoryFactories {
		t.Run(factory.name, func(t *testing.T) {
			ctx := context.Background()
			repo := factory.new(t)
			identity := QueueSessionIdentity{TaskID: "task-coalesce", SessionID: "session-coalesce", SessionIncarnationID: "incarnation-coalesce"}
			seedQueueSessionIdentity(t, repo, identity)
			seedCensusExecution(t, repo, identity)
			message := &QueuedMessage{
				ID: "entry-a", TaskID: identity.TaskID, SessionID: identity.SessionID,
				Content: "A", QueuedBy: QueuedByUser, Metadata: map[string]interface{}{MetadataCoalesceKey: "key"},
			}
			require.NoError(t, repo.InsertForSession(ctx, identity, message, 1))
			guarded := repo.(censusTransactionRepository)
			before, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			updated, replaced, err := repo.InsertOrReplaceByCoalesceKeyForSession(ctx, identity, &QueuedMessage{
				ID: "entry-b", TaskID: identity.TaskID, SessionID: identity.SessionID,
				Content: "B", QueuedBy: QueuedByUser, Metadata: map[string]interface{}{MetadataCoalesceKey: "key"},
			}, "key", 1, true)
			require.NoError(t, err)
			require.True(t, replaced)
			require.NotNil(t, updated)
			after, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			require.Greater(t, after.Revision, before.Revision)
			require.Len(t, after.Entries, 1)
		})
	}
}

func TestCensusRevisionFullCapacityAutomaticFold(t *testing.T) {
	for _, factory := range autoRunRepositoryFactories {
		t.Run(factory.name, func(t *testing.T) {
			ctx := context.Background()
			repo := factory.new(t)
			identity := QueueSessionIdentity{TaskID: "task-full-fold", SessionID: "session-full-fold", SessionIncarnationID: "incarnation-full-fold"}
			seedQueueSessionIdentity(t, repo, identity)
			seedCensusExecution(t, repo, identity)
			require.NoError(t, repo.InsertForSession(ctx, identity, &QueuedMessage{
				ID: "entry-a", TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "A", QueuedBy: QueuedByUser,
			}, 1))
			guarded := repo.(censusTransactionRepository)
			before, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", 1)
			require.NoError(t, err)
			admission := repo.(interface {
				AdmitQueueMessage(context.Context, QueueSessionIdentity, string, *QueuedMessage, *QueueAttachmentClaim, int, *AutoMergePolicy, *WorkflowEntryIdentity) (*QueuedMessage, bool, error)
			})
			accepted, replay, err := admission.AdmitQueueMessage(ctx, identity, "entry-b", &QueuedMessage{
				TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "B", QueuedBy: QueuedByUser,
			}, nil, 1, &AutoMergePolicy{Enabled: true, Source: AutoMergeSourceGlobal}, nil)
			require.NoError(t, err)
			require.False(t, replay)
			require.Equal(t, "entry-a", accepted.ID)
			after, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", 1)
			require.NoError(t, err)
			require.Len(t, after.Entries, 1)
			require.Greater(t, after.Revision, before.Revision)
			require.NotEqual(t, before.Entries[0].ClaimSHA256, after.Entries[0].ClaimSHA256)
		})
	}
}

func TestCensusRevisionTakeWriters(t *testing.T) {
	actions := []struct {
		name string
		act  func(context.Context, Repository, string, string) (*QueuedMessage, error)
	}{
		{name: "head", act: func(ctx context.Context, repo Repository, sessionID, _ string) (*QueuedMessage, error) {
			return repo.TakeHead(ctx, sessionID)
		}},
		{name: "by-id", act: func(ctx context.Context, repo Repository, sessionID, entryID string) (*QueuedMessage, error) {
			return repo.TakeByID(ctx, sessionID, entryID)
		}},
	}
	for _, factory := range autoRunRepositoryFactories {
		for _, action := range actions {
			t.Run(factory.name+"/"+action.name, func(t *testing.T) {
				ctx := context.Background()
				repo := factory.new(t)
				identity := QueueSessionIdentity{TaskID: "task-take", SessionID: "session-take", SessionIncarnationID: "incarnation-take"}
				seedQueueSessionIdentity(t, repo, identity)
				seedCensusExecution(t, repo, identity)
				require.NoError(t, repo.InsertForSession(ctx, identity, &QueuedMessage{
					ID: "entry-a", TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "A", QueuedBy: QueuedByUser,
				}, DefaultMaxPerSession))
				guarded := repo.(censusTransactionRepository)
				before, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
				require.NoError(t, err)
				got, err := action.act(ctx, repo, identity.SessionID, "entry-a")
				require.NoError(t, err)
				require.NotNil(t, got)
				after, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
				require.NoError(t, err)
				require.Empty(t, after.Entries)
				require.Greater(t, after.Revision, before.Revision)
			})
		}
	}
}

func TestCensusRevisionMergeWriter(t *testing.T) {
	for _, factory := range autoRunRepositoryFactories {
		t.Run(factory.name, func(t *testing.T) {
			ctx := context.Background()
			repo := factory.new(t)
			identity := QueueSessionIdentity{TaskID: "task-merge", SessionID: "session-merge", SessionIncarnationID: "incarnation-merge"}
			seedQueueSessionIdentity(t, repo, identity)
			seedCensusExecution(t, repo, identity)
			for _, id := range []string{"entry-a", "entry-b"} {
				require.NoError(t, repo.InsertForSession(ctx, identity, &QueuedMessage{
					ID: id, TaskID: identity.TaskID, SessionID: identity.SessionID, Content: id, QueuedBy: QueuedByUser,
				}, DefaultMaxPerSession))
			}
			guarded := repo.(censusTransactionRepository)
			before, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			_, err = repo.MergeIntoAboveForSession(ctx, identity, "entry-b", QueuedByUser)
			require.NoError(t, err)
			after, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			require.Len(t, after.Entries, 1)
			require.Greater(t, after.Revision, before.Revision)
		})
	}
}

func TestCensusRevisionSendNowWriters(t *testing.T) {
	for _, factory := range autoRunRepositoryFactories {
		t.Run(factory.name, func(t *testing.T) {
			ctx := context.Background()
			repo := factory.new(t)
			identity := QueueSessionIdentity{TaskID: "task-send-now", SessionID: "session-send-now", SessionIncarnationID: "incarnation-send-now"}
			seedQueueSessionIdentity(t, repo, identity)
			seedCensusExecution(t, repo, identity)
			require.NoError(t, repo.InsertForSession(ctx, identity, &QueuedMessage{
				ID: "entry-a", TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "A", QueuedBy: QueuedByUser,
			}, DefaultMaxPerSession))
			guarded := repo.(censusTransactionRepository)
			before, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			stored, err := repo.ListBySession(ctx, identity.SessionID)
			require.NoError(t, err)
			claim, err := repo.ClaimSendNowForSession(ctx, identity, stored)
			require.NoError(t, err)
			afterClaim, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			require.Greater(t, afterClaim.Revision, before.Revision)
			require.NoError(t, repo.RestoreSendNowClaim(ctx, claim))
			afterRestore, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			require.Greater(t, afterRestore.Revision, afterClaim.Revision)
			require.Len(t, afterRestore.Entries, 1)
		})
	}
}

func TestCensusRevisionSendNowAcknowledgeWriter(t *testing.T) {
	for _, factory := range autoRunRepositoryFactories {
		t.Run(factory.name, func(t *testing.T) {
			ctx := context.Background()
			repo := factory.new(t)
			identity := QueueSessionIdentity{TaskID: "task-send-ack", SessionID: "session-send-ack", SessionIncarnationID: "incarnation-send-ack"}
			seedQueueSessionIdentity(t, repo, identity)
			seedCensusExecution(t, repo, identity)
			require.NoError(t, repo.InsertForSession(ctx, identity, &QueuedMessage{
				ID: "entry-a", TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "A", QueuedBy: QueuedByUser,
				Metadata: map[string]interface{}{MetadataLifecycleDurable: true},
			}, DefaultMaxPerSession))
			stored, err := repo.ListBySession(ctx, identity.SessionID)
			require.NoError(t, err)
			claim, err := repo.ClaimSendNowForSession(ctx, identity, stored)
			require.NoError(t, err)
			guarded := repo.(censusTransactionRepository)
			beforeAck, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			require.NoError(t, repo.AcknowledgeSendNowClaim(ctx, claim))
			afterAck, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			require.Greater(t, afterAck.Revision, beforeAck.Revision)
			require.Empty(t, afterAck.Entries)
		})
	}
}

func TestCensusRevisionOrdinarySendNowAcknowledgeIsNoop(t *testing.T) {
	for _, factory := range autoRunRepositoryFactories {
		t.Run(factory.name, func(t *testing.T) {
			ctx := context.Background()
			repo := factory.new(t)
			identity := QueueSessionIdentity{TaskID: "task-ordinary-ack", SessionID: "session-ordinary-ack", SessionIncarnationID: "incarnation-ordinary-ack"}
			seedQueueSessionIdentity(t, repo, identity)
			seedCensusExecution(t, repo, identity)
			require.NoError(t, repo.InsertForSession(ctx, identity, &QueuedMessage{
				ID: "entry-a", TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "A", QueuedBy: QueuedByUser,
			}, DefaultMaxPerSession))
			stored, err := repo.ListBySession(ctx, identity.SessionID)
			require.NoError(t, err)
			claim, err := repo.ClaimSendNowForSession(ctx, identity, stored)
			require.NoError(t, err)
			guarded := repo.(censusTransactionRepository)
			beforeAck, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			require.NoError(t, repo.AcknowledgeSendNowClaim(ctx, claim))
			afterAck, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			require.Equal(t, beforeAck.Revision, afterAck.Revision)
			require.Empty(t, afterAck.Entries)
		})
	}
}

func TestCensusRevisionReservationAndReceiptWriters(t *testing.T) {
	for _, factory := range autoRunRepositoryFactories {
		t.Run(factory.name, func(t *testing.T) {
			ctx := context.Background()
			repo := factory.new(t)
			identity := QueueSessionIdentity{TaskID: "task-receipt", SessionID: "session-receipt", SessionIncarnationID: "incarnation-receipt"}
			seedQueueSessionIdentity(t, repo, identity)
			seedCensusExecution(t, repo, identity)
			require.NoError(t, repo.InsertForSession(ctx, identity, &QueuedMessage{
				ID: "entry-a", TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "A", QueuedBy: QueuedByWorkflow,
				Metadata: map[string]interface{}{MetadataLifecycleDurable: true},
			}, DefaultMaxPerSession))
			guarded := repo.(censusTransactionRepository)
			before, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			reserved, enabled, err := repo.ReserveHeadForDeliveryIfAutoRunForSession(ctx, identity)
			require.NoError(t, err)
			require.True(t, enabled)
			require.NotNil(t, reserved)
			afterReserve, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			require.Greater(t, afterReserve.Revision, before.Revision)
			require.NoError(t, repo.AcknowledgeByIDForSession(ctx, identity, reserved))
			afterAck, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			require.Greater(t, afterAck.Revision, afterReserve.Revision)
		})
	}
}

func TestCensusRevisionTakePendingMove(t *testing.T) {
	for _, factory := range autoRunRepositoryFactories {
		t.Run(factory.name, func(t *testing.T) {
			ctx := context.Background()
			repo := factory.new(t)
			identity := QueueSessionIdentity{TaskID: "task-move", SessionID: "session-move", SessionIncarnationID: "incarnation-move"}
			seedQueueSessionIdentity(t, repo, identity)
			seedCensusExecution(t, repo, identity)
			guarded := repo.(censusTransactionRepository)
			require.NoError(t, repo.SetPendingMove(ctx, identity.SessionID, &PendingMove{
				TaskID: identity.TaskID, SessionIncarnationID: identity.SessionIncarnationID,
			}))
			before, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			move, err := repo.TakePendingMove(ctx, identity.SessionID)
			require.NoError(t, err)
			require.NotNil(t, move)
			after, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			require.Greater(t, after.Revision, before.Revision)
		})
	}
}

func TestCensusRevisionManagedInputSettlementAndReplay(t *testing.T) {
	for _, factory := range autoRunRepositoryFactories {
		t.Run(factory.name, func(t *testing.T) {
			ctx := context.Background()
			repo := factory.new(t)
			identity := managedInputIdentity("census-" + factory.name)
			seedQueueSessionIdentity(t, repo, identity)
			seedCensusExecution(t, repo, identity)
			storage := requireManagedInputStorage(t, repo)
			receipt, replayed, err := storage.AdmitManagedInput(ctx, identity,
				managedInputRequest("input-1", "occurrence-1", "message", ManagedInputOriginHuman, ""), 10)
			require.NoError(t, err)
			require.False(t, replayed)
			guarded := repo.(censusTransactionRepository)
			before, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			require.Len(t, before.Entries, 1)
			_, changed, err := storage.MarkManagedInputRunning(ctx, identity, receipt.ID, "turn-1", "execution-1")
			require.NoError(t, err)
			require.True(t, changed)
			afterStart, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			require.Greater(t, afterStart.Revision, before.Revision)
			_, changed, err = storage.MarkManagedInputRunning(ctx, identity, receipt.ID, "turn-1", "execution-1")
			require.NoError(t, err)
			require.False(t, changed)
			afterReplay, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			require.Equal(t, afterStart.Revision, afterReplay.Revision)
			_, changed, err = storage.SettleManagedInput(ctx, identity, receipt.ID, "turn-1", "execution-1", ManagedInputStateCompleted, "delivered")
			require.NoError(t, err)
			require.True(t, changed)
			afterSettle, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			require.Greater(t, afterSettle.Revision, afterReplay.Revision)
		})
	}
}
