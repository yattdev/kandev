package messagequeue

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/kandev/kandev/internal/common/logger"
	"github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

// censusTransactionRepository is the repository boundary exercised by the
// original-execution queue tools, including their final conditional effect.
type censusTransactionRepository interface {
	ObservePendingCensus(context.Context, QueueSessionIdentity, string, int) (PendingCensus, error)
	DeleteClaimedPending(context.Context, QueueSessionIdentity, string, string, string) (*QueueRemovalResult, error)
}

func TestCensusClaimTransactionServiceSamplesCapacityAndRemoves(t *testing.T) {
	ctx := context.Background()
	repo := newTestSQLiteRepo(t)
	identity := QueueSessionIdentity{TaskID: "task-service", SessionID: "session-service", SessionIncarnationID: "incarnation-service"}
	seedQueueSessionIdentity(t, repo, identity)
	seedCensusExecution(t, repo, identity)
	service := NewService(repo, 7, logger.Default())
	require.NoError(t, repo.InsertForSession(ctx, identity, &QueuedMessage{
		ID: "entry-a", TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "A", QueuedBy: QueuedByUser,
	}, DefaultMaxPerSession))
	census, err := service.ObservePendingCensus(ctx, identity, "execution-1")
	require.NoError(t, err)
	require.Equal(t, 7, census.Capacity)
	removed, err := service.RemoveClaimedPending(ctx, identity, "execution-1", "entry-a", census.Entries[0].ClaimSHA256)
	require.NoError(t, err)
	require.Len(t, removed.Removed, 1)
}

type censusTestAuthority struct {
	mu      sync.Mutex
	current map[string]censusTestOwner
}

type blockedCensusAuthority struct {
	inner   *censusTestAuthority
	entered chan struct{}
	release chan struct{}
}

func (a *blockedCensusAuthority) WithinCurrentExecution(ctx context.Context, identity QueueSessionIdentity, executionID, workspaceID, ownerID string, effect func() error) error {
	close(a.entered)
	select {
	case <-a.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	return a.inner.WithinCurrentExecution(ctx, identity, executionID, workspaceID, ownerID, effect)
}

type censusTestOwner struct {
	identity    QueueSessionIdentity
	executionID string
	workspaceID string
	ownerID     string
}

func TestCensusClaimTransactionMemoryOwnerFence(t *testing.T) {
	ctx := context.Background()
	identity := QueueSessionIdentity{TaskID: "task-owner", SessionID: "session-owner", SessionIncarnationID: "incarnation-owner"}
	authority := &censusTestAuthority{current: map[string]censusTestOwner{identity.SessionID: {
		identity: identity, executionID: "execution-1", workspaceID: "workspace-1", ownerID: "owner-a",
	}}}
	repo := NewMemoryRepositoryWithCensusGuard(authority).(*memoryRepository)
	seedQueueSessionIdentity(t, repo, identity)
	require.NoError(t, repo.InsertForSession(ctx, identity, &QueuedMessage{
		ID: "entry-1", TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "keep", QueuedBy: QueuedByUser,
	}, DefaultMaxPerSession))
	ownerCtx := WithCensusOwnerFence(WithCensusWorkspaceFence(ctx, "workspace-1"), "owner-a")
	before, err := repo.ObservePendingCensus(ownerCtx, identity, "execution-1", 0)
	require.NoError(t, err)
	authority.mu.Lock()
	current := authority.current[identity.SessionID]
	current.ownerID = "owner-b"
	authority.current[identity.SessionID] = current
	require.NoError(t, repo.AdvanceCensusRevisionForAuthorityChange(identity.SessionID))
	authority.mu.Unlock()
	_, err = repo.DeleteClaimedPending(ownerCtx, identity, "execution-1", "entry-1", before.Entries[0].ClaimSHA256)
	require.ErrorIs(t, err, ErrSessionIdentityMismatch)
	entries, err := repo.ListBySession(ctx, identity.SessionID)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	authority.mu.Lock()
	current.ownerID = "owner-a"
	authority.current[identity.SessionID] = current
	require.NoError(t, repo.AdvanceCensusRevisionForAuthorityChange(identity.SessionID))
	authority.mu.Unlock()
	_, err = repo.DeleteClaimedPending(ownerCtx, identity, "execution-1", "entry-1", before.Entries[0].ClaimSHA256)
	require.ErrorIs(t, err, ErrQueueChanged)
	after, err := repo.ObservePendingCensus(ownerCtx, identity, "execution-1", 0)
	require.NoError(t, err)
	_, err = repo.DeleteClaimedPending(ownerCtx, identity, "execution-1", "entry-1", after.Entries[0].ClaimSHA256)
	require.NoError(t, err)
}

func (a *censusTestAuthority) WithinCurrentExecution(ctx context.Context, identity QueueSessionIdentity, executionID, workspaceID, ownerID string, effect func() error) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	owner, ok := a.current[identity.SessionID]
	if !ok || owner.identity != identity || owner.executionID != executionID ||
		(workspaceID != "" && owner.workspaceID != workspaceID) || (ownerID != "" && owner.ownerID != ownerID) {
		return ErrSessionIdentityMismatch
	}
	return effect()
}

func seedCensusExecution(t *testing.T, repo Repository, identity QueueSessionIdentity) {
	t.Helper()
	switch typed := repo.(type) {
	case *memoryRepository:
		authority, ok := typed.censusAuthority.(*censusTestAuthority)
		if !ok {
			authority = &censusTestAuthority{current: make(map[string]censusTestOwner)}
			typed.censusAuthority = authority
		}
		authority.mu.Lock()
		authority.current[identity.SessionID] = censusTestOwner{identity: identity, executionID: "execution-1"}
		authority.mu.Unlock()
	case *sqliteRepository:
		_, err := typed.db.Exec(`CREATE TABLE IF NOT EXISTS executors_running (session_id TEXT PRIMARY KEY, task_id TEXT NOT NULL, agent_execution_id TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'running')`)
		require.NoError(t, err)
		_, err = typed.db.Exec(typed.db.Rebind(`INSERT INTO executors_running (session_id, task_id, agent_execution_id) VALUES (?, ?, ?)
			ON CONFLICT(session_id) DO UPDATE SET task_id = excluded.task_id, agent_execution_id = excluded.agent_execution_id`), identity.SessionID, identity.TaskID, "execution-1")
		require.NoError(t, err)
	default:
		t.Fatalf("unsupported census repository %T", repo)
	}
}

func TestCensusClaimTransactionContentEdit(t *testing.T) {
	for _, factory := range autoRunRepositoryFactories {
		t.Run(factory.name, func(t *testing.T) {
			ctx := context.Background()
			repo := factory.new(t)
			identity := QueueSessionIdentity{
				TaskID: "task-census", SessionID: "session-census", SessionIncarnationID: "incarnation-census",
			}
			seedQueueSessionIdentity(t, repo, identity)
			seedCensusExecution(t, repo, identity)
			message := &QueuedMessage{
				ID: "entry-a", TaskID: identity.TaskID, SessionID: identity.SessionID,
				Content: "before", QueuedBy: QueuedByUser,
				Attachments: []MessageAttachment{{Type: "image", AttachmentID: "attachment-a", MimeType: "image/png"}},
			}
			require.NoError(t, repo.InsertForSession(ctx, identity, message, DefaultMaxPerSession))

			guarded, ok := repo.(censusTransactionRepository)
			require.True(t, ok, "repository must expose the guarded census transaction")
			original, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			require.Len(t, original.Entries, 1)
			require.Equal(t, message.ID, original.Entries[0].ID)
			digest := sha256.Sum256([]byte("before"))
			require.Equal(t, hex.EncodeToString(digest[:]), original.Entries[0].ContentHash)
			require.Equal(t, len("before"), original.Entries[0].ContentSize)

			require.NoError(t, repo.UpdateContentAndMetadataForSession(
				ctx, identity, message.ID, "after", message.Attachments, nil, QueuedByUser,
			))
			afterEdit, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			require.Equal(t, original.Entries[0].ID, afterEdit.Entries[0].ID)
			require.Equal(t, original.Entries[0].Position, afterEdit.Entries[0].Position)
			require.Greater(t, afterEdit.Revision, original.Revision)
			require.NotEqual(t, original.Entries[0].ClaimSHA256, afterEdit.Entries[0].ClaimSHA256)

			_, err = guarded.DeleteClaimedPending(ctx, identity, "execution-1", message.ID, original.Entries[0].ClaimSHA256)
			require.ErrorIs(t, err, ErrQueueChanged)
			stored, err := repo.ListBySession(ctx, identity.SessionID)
			require.NoError(t, err)
			require.Len(t, stored, 1)
			require.Equal(t, "after", stored[0].Content)
			require.Equal(t, message.Attachments, stored[0].Attachments)

			removed, err := guarded.DeleteClaimedPending(ctx, identity, "execution-1", message.ID, afterEdit.Entries[0].ClaimSHA256)
			require.NoError(t, err)
			require.Len(t, removed.Removed, 1)
			require.Equal(t, message.ID, removed.Removed[0].ID)
			_, err = guarded.DeleteClaimedPending(ctx, identity, "execution-1", message.ID, afterEdit.Entries[0].ClaimSHA256)
			require.Error(t, err)
			stored, err = repo.ListBySession(ctx, identity.SessionID)
			require.NoError(t, err)
			require.Empty(t, stored)
		})
	}
}

func TestCensusClaimTransactionObservationUsesReadOnlyDatabase(t *testing.T) {
	ctx := context.Background()
	repo := newTestSQLiteRepo(t).(*sqliteRepository)
	identity := QueueSessionIdentity{TaskID: "task-read-only", SessionID: "session-read-only", SessionIncarnationID: "incarnation-read-only"}
	seedQueueSessionIdentity(t, repo, identity)
	seedCensusExecution(t, repo, identity)
	require.NoError(t, repo.InsertForSession(ctx, identity, &QueuedMessage{
		ID: "entry-a", TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "body", QueuedBy: QueuedByUser,
	}, DefaultMaxPerSession))
	var writes atomic.Int64
	installAuthorizer := func(callback func(int, string, string, string) int) {
		connection, connErr := repo.db.Conn(ctx)
		require.NoError(t, connErr)
		require.NoError(t, connection.Raw(func(driverConn any) error {
			driverConn.(*sqlite3.SQLiteConn).RegisterAuthorizer(callback)
			return nil
		}))
		require.NoError(t, connection.Close())
	}
	installAuthorizer(func(operation int, _, _, _ string) int {
		switch operation {
		case sqlite3.SQLITE_INSERT, sqlite3.SQLITE_UPDATE, sqlite3.SQLITE_DELETE,
			sqlite3.SQLITE_CREATE_TABLE, sqlite3.SQLITE_ALTER_TABLE, sqlite3.SQLITE_DROP_TABLE,
			sqlite3.SQLITE_CREATE_INDEX, sqlite3.SQLITE_DROP_INDEX,
			sqlite3.SQLITE_CREATE_TRIGGER, sqlite3.SQLITE_DROP_TRIGGER:
			writes.Add(1)
			return sqlite3.SQLITE_DENY
		}
		return sqlite3.SQLITE_OK
	})
	t.Cleanup(func() { installAuthorizer(nil) })
	_, err := repo.db.ExecContext(ctx, `UPDATE queue_session_locks SET census_revision = census_revision`)
	require.Error(t, err, "authorizer must observe and deny a write")
	require.Positive(t, writes.Load())
	writes.Store(0)
	_, err = repo.db.ExecContext(ctx, `PRAGMA query_only = ON`)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = repo.db.Exec(`PRAGMA query_only = OFF`) })
	census, err := repo.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
	require.NoError(t, err)
	require.Len(t, census.Entries, 1)
	require.Zero(t, writes.Load(), "census observation must issue no DML or DDL")
}

func TestCensusClaimTransactionSQLiteFailedRevisionRollsBackEdit(t *testing.T) {
	ctx := context.Background()
	repo := newTestSQLiteRepo(t).(*sqliteRepository)
	identity := QueueSessionIdentity{TaskID: "task-rollback", SessionID: "session-rollback", SessionIncarnationID: "incarnation-rollback"}
	seedQueueSessionIdentity(t, repo, identity)
	seedCensusExecution(t, repo, identity)
	require.NoError(t, repo.InsertForSession(ctx, identity, &QueuedMessage{
		ID: "entry-a", TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "before", QueuedBy: QueuedByUser,
	}, DefaultMaxPerSession))
	before, err := repo.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
	require.NoError(t, err)
	_, err = repo.db.Exec(`CREATE TRIGGER fail_census_revision BEFORE UPDATE OF census_revision ON queue_session_locks BEGIN SELECT RAISE(ABORT, 'forced revision failure'); END`)
	require.NoError(t, err)
	err = repo.UpdateContentAndMetadataForSession(ctx, identity, "entry-a", "after", nil, nil, QueuedByUser)
	require.ErrorContains(t, err, "forced revision failure")
	_, err = repo.db.Exec(`DROP TRIGGER fail_census_revision`)
	require.NoError(t, err)
	after, err := repo.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
	require.NoError(t, err)
	require.Equal(t, before.Revision, after.Revision)
	require.Equal(t, before.Entries[0].ContentHash, after.Entries[0].ContentHash)
	require.Equal(t, before.Entries[0].ClaimSHA256, after.Entries[0].ClaimSHA256)
}

func TestCensusClaimTransactionSQLiteRevisionOverflowRollsBackEdit(t *testing.T) {
	ctx := context.Background()
	repo := newTestSQLiteRepo(t).(*sqliteRepository)
	identity := QueueSessionIdentity{TaskID: "task-overflow-edit", SessionID: "session-overflow-edit", SessionIncarnationID: "incarnation-overflow-edit"}
	seedQueueSessionIdentity(t, repo, identity)
	seedCensusExecution(t, repo, identity)
	require.NoError(t, repo.InsertForSession(ctx, identity, &QueuedMessage{
		ID: "entry-a", TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "before", QueuedBy: QueuedByUser,
	}, DefaultMaxPerSession))
	_, err := repo.db.Exec(repo.db.Rebind(`UPDATE queue_session_locks SET census_revision = ? WHERE session_id = ?`), int64(math.MaxInt64), identity.SessionID)
	require.NoError(t, err)
	before, err := repo.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
	require.NoError(t, err)
	err = repo.UpdateContentAndMetadataForSession(ctx, identity, "entry-a", "after", nil, nil, QueuedByUser)
	require.ErrorIs(t, err, errCensusRevisionExhausted)
	after, err := repo.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
	require.NoError(t, err)
	require.Equal(t, before.Revision, after.Revision)
	require.Equal(t, before.Entries[0].ContentHash, after.Entries[0].ContentHash)
	require.Equal(t, before.Entries[0].ClaimSHA256, after.Entries[0].ClaimSHA256)
}

func TestCensusClaimTransactionSQLiteFailedCommitRollsBackRevision(t *testing.T) {
	ctx := context.Background()
	repo := newTestSQLiteRepo(t).(*sqliteRepository)
	identity := QueueSessionIdentity{TaskID: "task-commit-failure", SessionID: "session-commit-failure", SessionIncarnationID: "incarnation-commit-failure"}
	seedQueueSessionIdentity(t, repo, identity)
	seedCensusExecution(t, repo, identity)
	require.NoError(t, repo.InsertForSession(ctx, identity, &QueuedMessage{
		ID: "entry-a", TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "before", QueuedBy: QueuedByUser,
	}, DefaultMaxPerSession))
	before, err := repo.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
	require.NoError(t, err)
	_, err = repo.db.ExecContext(ctx, `CREATE TABLE census_commit_parent (id TEXT PRIMARY KEY)`)
	require.NoError(t, err)
	_, err = repo.db.ExecContext(ctx, `CREATE TABLE census_commit_child (id TEXT PRIMARY KEY, parent_id TEXT REFERENCES census_commit_parent(id) DEFERRABLE INITIALLY DEFERRED)`)
	require.NoError(t, err)
	tx, err := repo.db.BeginTxx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `INSERT INTO census_commit_child (id, parent_id) VALUES ('child-1', 'missing')`)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, repo.db.Rebind(`UPDATE queued_messages SET content = ? WHERE id = ?`), "after", "entry-a")
	require.NoError(t, err)
	require.NoError(t, bumpCensusRevisionTx(ctx, tx, repo.db, identity.SessionID))
	require.Error(t, tx.Commit())
	_ = tx.Rollback()
	after, err := repo.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
	require.NoError(t, err)
	require.Equal(t, before, after)
}

func TestCensusClaimTransactionSQLiteSavepointRollbackRetainsClaim(t *testing.T) {
	ctx := context.Background()
	repo := newTestSQLiteRepo(t).(*sqliteRepository)
	identity := QueueSessionIdentity{TaskID: "task-savepoint", SessionID: "session-savepoint", SessionIncarnationID: "incarnation-savepoint"}
	seedQueueSessionIdentity(t, repo, identity)
	seedCensusExecution(t, repo, identity)
	require.NoError(t, repo.InsertForSession(ctx, identity, &QueuedMessage{
		ID: "entry-a", TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "before", QueuedBy: QueuedByUser,
	}, DefaultMaxPerSession))
	before, err := repo.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
	require.NoError(t, err)
	tx, err := repo.db.BeginTxx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `SAVEPOINT census_edit`)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, repo.db.Rebind(`UPDATE queued_messages SET content = ? WHERE id = ?`), "after", "entry-a")
	require.NoError(t, err)
	require.NoError(t, bumpCensusRevisionTx(ctx, tx, repo.db, identity.SessionID))
	_, err = tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT census_edit`)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `RELEASE SAVEPOINT census_edit`)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	after, err := repo.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
	require.NoError(t, err)
	require.Equal(t, before, after)
}

func TestCensusClaimTransactionSQLiteSchemaReplayRetainsRevision(t *testing.T) {
	ctx := context.Background()
	repo := newTestSQLiteRepo(t).(*sqliteRepository)
	identity := QueueSessionIdentity{TaskID: "task-reopen", SessionID: "session-reopen", SessionIncarnationID: "incarnation-reopen"}
	seedQueueSessionIdentity(t, repo, identity)
	seedCensusExecution(t, repo, identity)
	require.NoError(t, repo.InsertForSession(ctx, identity, &QueuedMessage{
		ID: "entry-a", TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "A", QueuedBy: QueuedByUser,
	}, DefaultMaxPerSession))
	before, err := repo.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
	require.NoError(t, err)
	reopened, err := NewSQLiteRepository(repo.db, repo.ro)
	require.NoError(t, err)
	after, err := reopened.(censusTransactionRepository).ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
	require.NoError(t, err)
	require.Equal(t, before.Revision, after.Revision)
	require.Equal(t, before.Entries[0].ClaimSHA256, after.Entries[0].ClaimSHA256)
}

func TestCensusClaimTransactionEnqueueAndABA(t *testing.T) {
	for _, factory := range autoRunRepositoryFactories {
		t.Run(factory.name, func(t *testing.T) {
			ctx := context.Background()
			repo := factory.new(t)
			identity := QueueSessionIdentity{
				TaskID: "task-census-aba", SessionID: "session-census-aba", SessionIncarnationID: "incarnation-census-aba",
			}
			seedQueueSessionIdentity(t, repo, identity)
			seedCensusExecution(t, repo, identity)
			guarded := repo.(censusTransactionRepository)
			a := &QueuedMessage{ID: "entry-a", TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "A", QueuedBy: QueuedByUser}
			require.NoError(t, repo.InsertForSession(ctx, identity, a, DefaultMaxPerSession))
			first, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)

			b := &QueuedMessage{ID: "entry-b", TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "B", QueuedBy: QueuedByUser}
			require.NoError(t, repo.InsertForSession(ctx, identity, b, DefaultMaxPerSession))
			second, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			require.Greater(t, second.Revision, first.Revision)
			_, err = guarded.DeleteClaimedPending(ctx, identity, "execution-1", a.ID, first.Entries[0].ClaimSHA256)
			require.ErrorIs(t, err, ErrQueueChanged)

			require.NoError(t, repo.DeleteByID(ctx, identity.SessionID, b.ID))
			third, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			require.Len(t, third.Entries, 1)
			require.Equal(t, first.Entries[0].ID, third.Entries[0].ID)
			require.Equal(t, first.Entries[0].Position, third.Entries[0].Position)
			require.Greater(t, third.Revision, second.Revision)
			_, err = guarded.DeleteClaimedPending(ctx, identity, "execution-1", a.ID, first.Entries[0].ClaimSHA256)
			require.ErrorIs(t, err, ErrQueueChanged)
			stored, err := repo.ListBySession(ctx, identity.SessionID)
			require.NoError(t, err)
			require.Len(t, stored, 1)
			require.Equal(t, "A", stored[0].Content)
		})
	}
}

func TestCensusClaimTransactionMemoryOrdersEqualPositionsByID(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryRepository().(*memoryRepository)
	identity := QueueSessionIdentity{TaskID: "task-order", SessionID: "session-order", SessionIncarnationID: "incarnation-order"}
	seedQueueSessionIdentity(t, repo, identity)
	seedCensusExecution(t, repo, identity)
	for _, id := range []string{"entry-b", "entry-a"} {
		require.NoError(t, repo.InsertForSession(ctx, identity, &QueuedMessage{
			ID: id, TaskID: identity.TaskID, SessionID: identity.SessionID, Content: id, QueuedBy: QueuedByUser,
		}, DefaultMaxPerSession))
	}
	// An imported legacy queue can have tied positions; the observer still
	// exposes the contract's deterministic (position, id) order.
	repo.mu.Lock()
	repo.entries[identity.SessionID][1].Position = repo.entries[identity.SessionID][0].Position
	repo.mu.Unlock()
	census, err := repo.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
	require.NoError(t, err)
	require.Equal(t, []string{"entry-a", "entry-b"}, []string{census.Entries[0].ID, census.Entries[1].ID})
}

func TestCensusClaimTransactionTwoRemoversAtMostOneSuccess(t *testing.T) {
	for _, factory := range autoRunRepositoryFactories {
		t.Run(factory.name, func(t *testing.T) {
			ctx := context.Background()
			repo := factory.new(t)
			identity := QueueSessionIdentity{TaskID: "task-two-removers", SessionID: "session-two-removers", SessionIncarnationID: "incarnation-two-removers"}
			seedQueueSessionIdentity(t, repo, identity)
			seedCensusExecution(t, repo, identity)
			require.NoError(t, repo.InsertForSession(ctx, identity, &QueuedMessage{
				ID: "entry-a", TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "A", QueuedBy: QueuedByUser,
			}, DefaultMaxPerSession))
			guarded := repo.(censusTransactionRepository)
			before, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			start := make(chan struct{})
			outcomes := make(chan error, 2)
			for range 2 {
				go func() {
					<-start
					_, removeErr := guarded.DeleteClaimedPending(ctx, identity, "execution-1", "entry-a", before.Entries[0].ClaimSHA256)
					outcomes <- removeErr
				}()
			}
			close(start)
			first, second := <-outcomes, <-outcomes
			require.True(t, (first == nil) != (second == nil), "exactly one remover must commit: %v / %v", first, second)
			if first != nil {
				require.ErrorIs(t, first, ErrQueueChanged)
			}
			if second != nil {
				require.ErrorIs(t, second, ErrQueueChanged)
			}
			entries, err := repo.ListBySession(ctx, identity.SessionID)
			require.NoError(t, err)
			require.Empty(t, entries)
		})
	}
}

func TestCensusClaimTransactionResponseLossRetryKeepsSuccessor(t *testing.T) {
	for _, factory := range autoRunRepositoryFactories {
		t.Run(factory.name, func(t *testing.T) {
			ctx := context.Background()
			repo := factory.new(t)
			identity := QueueSessionIdentity{TaskID: "task-response-loss", SessionID: "session-response-loss", SessionIncarnationID: "incarnation-response-loss"}
			seedQueueSessionIdentity(t, repo, identity)
			seedCensusExecution(t, repo, identity)
			guarded := repo.(censusTransactionRepository)
			require.NoError(t, repo.InsertForSession(ctx, identity, &QueuedMessage{
				ID: "entry-a", TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "old", QueuedBy: QueuedByUser,
			}, DefaultMaxPerSession))
			before, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			_, err = guarded.DeleteClaimedPending(ctx, identity, "execution-1", "entry-a", before.Entries[0].ClaimSHA256)
			require.NoError(t, err)
			require.NoError(t, repo.InsertForSession(ctx, identity, &QueuedMessage{
				ID: "entry-a", TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "successor", QueuedBy: QueuedByUser,
			}, DefaultMaxPerSession))
			_, err = guarded.DeleteClaimedPending(ctx, identity, "execution-1", "entry-a", before.Entries[0].ClaimSHA256)
			require.ErrorIs(t, err, ErrQueueChanged)
			entries, err := repo.ListBySession(ctx, identity.SessionID)
			require.NoError(t, err)
			require.Len(t, entries, 1)
			require.Equal(t, "successor", entries[0].Content)
		})
	}
}

func TestCensusClaimTransactionMemoryRotationBetweenIngressAndEffect(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryRepository().(*memoryRepository)
	identity := QueueSessionIdentity{TaskID: "task-race", SessionID: "session-race", SessionIncarnationID: "incarnation-race"}
	seedQueueSessionIdentity(t, repo, identity)
	seedCensusExecution(t, repo, identity)
	require.NoError(t, repo.InsertForSession(ctx, identity, &QueuedMessage{
		ID: "entry-a", TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "A", QueuedBy: QueuedByUser,
	}, DefaultMaxPerSession))
	before, err := repo.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
	require.NoError(t, err)
	inner := repo.censusAuthority.(*censusTestAuthority)
	gate := &blockedCensusAuthority{inner: inner, entered: make(chan struct{}), release: make(chan struct{})}
	repo.censusAuthority = gate
	result := make(chan error, 1)
	go func() {
		_, removeErr := repo.DeleteClaimedPending(ctx, identity, "execution-1", "entry-a", before.Entries[0].ClaimSHA256)
		result <- removeErr
	}()
	<-gate.entered
	inner.mu.Lock()
	inner.current[identity.SessionID] = censusTestOwner{identity: identity, executionID: "execution-2"}
	inner.mu.Unlock()
	close(gate.release)
	require.ErrorIs(t, <-result, ErrSessionIdentityMismatch)
	stored, err := repo.ListBySession(ctx, identity.SessionID)
	require.NoError(t, err)
	require.Len(t, stored, 1)
	require.Equal(t, "A", stored[0].Content)
}

func TestCensusClaimTransactionRejectsIncompleteObservation(t *testing.T) {
	for _, factory := range autoRunRepositoryFactories {
		t.Run(factory.name, func(t *testing.T) {
			ctx := context.Background()
			repo := factory.new(t)
			identity := QueueSessionIdentity{
				TaskID: "task-census-limit", SessionID: "session-census-limit", SessionIncarnationID: "incarnation-census-limit",
			}
			seedQueueSessionIdentity(t, repo, identity)
			seedCensusExecution(t, repo, identity)
			for index := 0; index < 129; index++ {
				message := &QueuedMessage{TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "body", QueuedBy: QueuedByUser}
				require.NoError(t, repo.InsertForSession(ctx, identity, message, 0))
			}
			_, err := repo.(censusTransactionRepository).ObservePendingCensus(ctx, identity, "execution-1", 0)
			require.Error(t, err)
			stored, err := repo.ListBySession(ctx, identity.SessionID)
			require.NoError(t, err)
			require.Len(t, stored, 129)
		})
	}
}

func TestCensusClaimTransactionPolicyWriters(t *testing.T) {
	actions := []struct {
		name string
		act  func(context.Context, Repository, QueueSessionIdentity) error
	}{
		{name: "auto-run", act: func(ctx context.Context, repo Repository, identity QueueSessionIdentity) error {
			return repo.SetAutoRunForSession(ctx, identity, false)
		}},
		{name: "auto-merge-override", act: func(ctx context.Context, repo Repository, identity QueueSessionIdentity) error {
			_, err := repo.SetAutoMergeOverride(ctx, identity, true)
			return err
		}},
		{name: "pause-auto-run", act: func(ctx context.Context, repo Repository, identity QueueSessionIdentity) error {
			changed, err := repo.PauseAutoRunIfPendingForSession(ctx, identity)
			require.True(t, changed)
			return err
		}},
	}
	for _, factory := range autoRunRepositoryFactories {
		for _, action := range actions {
			t.Run(factory.name+"/"+action.name, func(t *testing.T) {
				ctx := context.Background()
				repo := factory.new(t)
				identity := QueueSessionIdentity{TaskID: "task-policy", SessionID: "session-policy", SessionIncarnationID: "incarnation-policy"}
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

func TestCensusClaimTransactionAppendWriter(t *testing.T) {
	for _, factory := range autoRunRepositoryFactories {
		t.Run(factory.name, func(t *testing.T) {
			ctx := context.Background()
			repo := factory.new(t)
			identity := QueueSessionIdentity{TaskID: "task-append", SessionID: "session-append", SessionIncarnationID: "incarnation-append"}
			seedQueueSessionIdentity(t, repo, identity)
			seedCensusExecution(t, repo, identity)
			message := &QueuedMessage{ID: "entry-a", TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "A", QueuedBy: QueuedByUser}
			require.NoError(t, repo.InsertForSession(ctx, identity, message, DefaultMaxPerSession))
			guarded := repo.(censusTransactionRepository)
			before, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			updated, appended, err := repo.AppendOrInsertTailForSession(ctx, identity, "B", "", QueuedByUser, false, nil, nil, DefaultMaxPerSession)
			require.NoError(t, err)
			require.True(t, appended)
			require.Equal(t, message.ID, updated.ID)
			after, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			require.Greater(t, after.Revision, before.Revision)
			_, err = guarded.DeleteClaimedPending(ctx, identity, "execution-1", message.ID, before.Entries[0].ClaimSHA256)
			require.ErrorIs(t, err, ErrQueueChanged)
		})
	}
}

func TestCensusClaimTransactionRejectsOversizeProjection(t *testing.T) {
	for _, factory := range autoRunRepositoryFactories {
		t.Run(factory.name, func(t *testing.T) {
			ctx := context.Background()
			repo := factory.new(t)
			identity := QueueSessionIdentity{TaskID: "task-large", SessionID: "session-large", SessionIncarnationID: "incarnation-large"}
			seedQueueSessionIdentity(t, repo, identity)
			seedCensusExecution(t, repo, identity)
			for index := 0; index < censusMaxVisibleRows; index++ {
				message := &QueuedMessage{
					ID:     fmt.Sprintf("entry-%03d-%s", index, strings.Repeat("x", 246)),
					TaskID: identity.TaskID, SessionID: identity.SessionID,
					Content: "body", QueuedBy: strings.Repeat("s", 256),
				}
				require.NoError(t, repo.InsertForSession(ctx, identity, message, 0))
			}
			_, err := repo.(censusTransactionRepository).ObservePendingCensus(ctx, identity, "execution-1", 0)
			require.Error(t, err)
		})
	}
}

func TestCensusClaimTransactionReserveReleaseAndReorder(t *testing.T) {
	for _, factory := range autoRunRepositoryFactories {
		t.Run(factory.name, func(t *testing.T) {
			ctx := context.Background()
			repo := factory.new(t)
			identity := QueueSessionIdentity{TaskID: "task-order", SessionID: "session-order", SessionIncarnationID: "incarnation-order"}
			seedQueueSessionIdentity(t, repo, identity)
			seedCensusExecution(t, repo, identity)
			for _, id := range []string{"a", "b"} {
				require.NoError(t, repo.InsertForSession(ctx, identity, &QueuedMessage{
					ID: id, TaskID: identity.TaskID, SessionID: identity.SessionID, Content: id, QueuedBy: QueuedByUser,
				}, DefaultMaxPerSession))
			}
			guarded := repo.(censusTransactionRepository)
			before, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			require.NoError(t, repo.ReorderEntriesForSession(ctx, identity, []string{"b", "a"}))
			reordered, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			require.Greater(t, reordered.Revision, before.Revision)
			require.Equal(t, "b", reordered.Entries[0].ID)
			_, err = guarded.DeleteClaimedPending(ctx, identity, "execution-1", "a", before.Entries[0].ClaimSHA256)
			require.ErrorIs(t, err, ErrQueueChanged)

			reserved, enabled, err := repo.ReserveHeadForDeliveryIfAutoRunForSession(ctx, identity)
			require.NoError(t, err)
			require.True(t, enabled)
			require.NotNil(t, reserved)
			during, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			require.Greater(t, during.Revision, reordered.Revision)
			require.Len(t, during.Entries, 1)
			require.NoError(t, repo.ReleaseDeliveryReservationForSession(ctx, identity, reserved))
			after, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			require.Greater(t, after.Revision, during.Revision)
			require.Len(t, after.Entries, 2)
		})
	}
}

func TestCensusClaimTransactionRejectsRotatedExecution(t *testing.T) {
	for _, factory := range autoRunRepositoryFactories {
		t.Run(factory.name, func(t *testing.T) {
			ctx := context.Background()
			repo := factory.new(t)
			identity := QueueSessionIdentity{TaskID: "task-rotation", SessionID: "session-rotation", SessionIncarnationID: "incarnation-rotation"}
			seedQueueSessionIdentity(t, repo, identity)
			seedCensusExecution(t, repo, identity)
			message := &QueuedMessage{ID: "entry-a", TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "A", QueuedBy: QueuedByUser}
			require.NoError(t, repo.InsertForSession(ctx, identity, message, DefaultMaxPerSession))
			guarded := repo.(censusTransactionRepository)
			before, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
			require.NoError(t, err)
			switch typed := repo.(type) {
			case *memoryRepository:
				authority := typed.censusAuthority.(*censusTestAuthority)
				authority.mu.Lock()
				authority.current[identity.SessionID] = censusTestOwner{identity: identity, executionID: "execution-2"}
				authority.mu.Unlock()
			case *sqliteRepository:
				_, err = typed.db.Exec(typed.db.Rebind(`UPDATE executors_running SET agent_execution_id = ? WHERE session_id = ?`), "execution-2", identity.SessionID)
				require.NoError(t, err)
			}
			_, err = guarded.DeleteClaimedPending(ctx, identity, "execution-1", message.ID, before.Entries[0].ClaimSHA256)
			require.Error(t, err)
			_, err = guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
			require.Error(t, err)
			after, err := guarded.ObservePendingCensus(ctx, identity, "execution-2", DefaultMaxPerSession)
			require.NoError(t, err)
			require.NotEqual(t, before.Entries[0].ClaimSHA256, after.Entries[0].ClaimSHA256)
			stored, err := repo.ListBySession(ctx, identity.SessionID)
			require.NoError(t, err)
			require.Len(t, stored, 1)
		})
	}
}

func TestCensusClaimTransactionRejectsStoppedExecution(t *testing.T) {
	ctx := context.Background()
	repo := newTestSQLiteRepo(t).(*sqliteRepository)
	identity := QueueSessionIdentity{TaskID: "task-stopped", SessionID: "session-stopped", SessionIncarnationID: "incarnation-stopped"}
	seedQueueSessionIdentity(t, repo, identity)
	seedCensusExecution(t, repo, identity)
	require.NoError(t, repo.InsertForSession(ctx, identity, &QueuedMessage{
		ID: "entry-a", TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "A", QueuedBy: QueuedByUser,
	}, DefaultMaxPerSession))
	before, err := repo.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
	require.NoError(t, err)
	_, err = repo.db.Exec(repo.db.Rebind(`UPDATE executors_running SET status = 'stopped' WHERE session_id = ?`), identity.SessionID)
	require.NoError(t, err)
	_, err = repo.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
	require.ErrorIs(t, err, ErrSessionIdentityMismatch)
	_, err = repo.DeleteClaimedPending(ctx, identity, "execution-1", "entry-a", before.Entries[0].ClaimSHA256)
	require.ErrorIs(t, err, ErrSessionIdentityMismatch)
	stored, err := repo.ListBySession(ctx, identity.SessionID)
	require.NoError(t, err)
	require.Len(t, stored, 1)
}

func TestCensusClaimTransactionRejectsWorkspaceChangeAtEffect(t *testing.T) {
	ctx := context.Background()
	repo := newTestSQLiteRepo(t).(*sqliteRepository)
	identity := QueueSessionIdentity{TaskID: "task-workspace-effect", SessionID: "session-workspace-effect", SessionIncarnationID: "incarnation-workspace-effect"}
	seedQueueSessionIdentity(t, repo, identity)
	seedCensusExecution(t, repo, identity)
	_, err := repo.db.ExecContext(ctx, `ALTER TABLE tasks ADD COLUMN workspace_id TEXT NOT NULL DEFAULT ''`)
	require.NoError(t, err)
	_, err = repo.db.ExecContext(ctx, repo.db.Rebind(`UPDATE tasks SET workspace_id = ? WHERE id = ?`), "workspace-1", identity.TaskID)
	require.NoError(t, err)
	require.NoError(t, repo.InsertForSession(ctx, identity, &QueuedMessage{
		ID: "entry-a", TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "A", QueuedBy: QueuedByUser,
	}, DefaultMaxPerSession))
	before, err := repo.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
	require.NoError(t, err)
	_, err = repo.db.ExecContext(ctx, repo.db.Rebind(`UPDATE tasks SET workspace_id = ? WHERE id = ?`), "workspace-2", identity.TaskID)
	require.NoError(t, err)
	_, err = repo.ObservePendingCensus(WithCensusWorkspaceFence(ctx, "workspace-1"), identity, "execution-1", DefaultMaxPerSession)
	require.ErrorIs(t, err, ErrSessionIdentityMismatch)
	_, err = repo.DeleteClaimedPending(WithCensusWorkspaceFence(ctx, "workspace-1"), identity,
		"execution-1", "entry-a", before.Entries[0].ClaimSHA256)
	require.ErrorIs(t, err, ErrSessionIdentityMismatch)
	rows, err := repo.ListBySession(ctx, identity.SessionID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
}

func TestCensusClaimTransactionRejectsForeignExecutionRow(t *testing.T) {
	ctx := context.Background()
	repo := newTestSQLiteRepo(t).(*sqliteRepository)
	identity := QueueSessionIdentity{TaskID: "task-owned-execution", SessionID: "session-owned-execution", SessionIncarnationID: "incarnation-owned-execution"}
	seedQueueSessionIdentity(t, repo, identity)
	seedCensusExecution(t, repo, identity)
	require.NoError(t, repo.InsertForSession(ctx, identity, &QueuedMessage{
		ID: "entry-a", TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "A", QueuedBy: QueuedByUser,
	}, DefaultMaxPerSession))
	before, err := repo.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
	require.NoError(t, err)
	_, err = repo.db.Exec(repo.db.Rebind(`UPDATE executors_running SET task_id = 'foreign-task' WHERE session_id = ?`), identity.SessionID)
	require.NoError(t, err)
	_, err = repo.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
	require.ErrorIs(t, err, ErrSessionIdentityMismatch)
	_, err = repo.DeleteClaimedPending(ctx, identity, "execution-1", "entry-a", before.Entries[0].ClaimSHA256)
	require.ErrorIs(t, err, ErrSessionIdentityMismatch)
}

func TestCensusClaimTransactionMemoryRequiresAuthorityGuard(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryRepository()
	identity := QueueSessionIdentity{TaskID: "task-guard", SessionID: "session-guard", SessionIncarnationID: "incarnation-guard"}
	seedQueueSessionIdentity(t, repo, identity)
	message := &QueuedMessage{ID: "entry-a", TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "A", QueuedBy: QueuedByUser}
	require.NoError(t, repo.InsertForSession(ctx, identity, message, DefaultMaxPerSession))
	guarded := repo.(censusTransactionRepository)
	_, err := guarded.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
	require.ErrorIs(t, err, ErrQueueAdmissionUnavailable)
	claim, err := CensusEntryClaim(identity, "execution-1", message.ID, 1)
	require.NoError(t, err)
	_, err = guarded.DeleteClaimedPending(ctx, identity, "execution-1", message.ID, claim)
	require.ErrorIs(t, err, ErrQueueAdmissionUnavailable)
	stored, err := repo.ListBySession(ctx, identity.SessionID)
	require.NoError(t, err)
	require.Len(t, stored, 1)
}
