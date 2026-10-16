package messagequeue

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type insideCensusAuthority struct {
	inner   *censusTestAuthority
	entered chan struct{}
}

func (a *insideCensusAuthority) WithinCurrentExecution(ctx context.Context, identity QueueSessionIdentity, executionID, workspaceID, ownerID string, effect func() error) error {
	return a.inner.WithinCurrentExecution(ctx, identity, executionID, workspaceID, ownerID, func() error {
		close(a.entered)
		return effect()
	})
}

func TestCensusClaimTransactionMemoryEffectCommitsBeforeRotation(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryRepository().(*memoryRepository)
	identity := QueueSessionIdentity{TaskID: "task-effect-first", SessionID: "session-effect-first", SessionIncarnationID: "incarnation-1"}
	seedQueueSessionIdentity(t, repo, identity)
	seedCensusExecution(t, repo, identity)
	require.NoError(t, repo.InsertForSession(ctx, identity, &QueuedMessage{
		ID: "entry-a", TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "A", QueuedBy: QueuedByUser,
	}, DefaultMaxPerSession))
	before, err := repo.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
	require.NoError(t, err)
	inner := repo.censusAuthority.(*censusTestAuthority)
	gate := &insideCensusAuthority{inner: inner, entered: make(chan struct{})}
	repo.censusAuthority = gate
	repo.mu.Lock()
	removed := make(chan error, 1)
	go func() {
		_, removeErr := repo.DeleteClaimedPending(ctx, identity, "execution-1", "entry-a", before.Entries[0].ClaimSHA256)
		removed <- removeErr
	}()
	<-gate.entered
	rotationStarted := make(chan struct{})
	rotationDone := make(chan struct{})
	go func() {
		close(rotationStarted)
		inner.mu.Lock()
		inner.current[identity.SessionID] = censusTestOwner{identity: identity, executionID: "execution-2"}
		inner.mu.Unlock()
		close(rotationDone)
	}()
	<-rotationStarted
	repo.mu.Unlock()
	require.NoError(t, <-removed)
	<-rotationDone
	rows, err := repo.ListBySession(ctx, identity.SessionID)
	require.NoError(t, err)
	require.Empty(t, rows)
	_, err = repo.DeleteClaimedPending(ctx, identity, "execution-1", "entry-a", before.Entries[0].ClaimSHA256)
	require.ErrorIs(t, err, ErrSessionIdentityMismatch)
}

func TestCensusClaimTransactionMemoryWorkspaceRotationRefusesRetainedStream(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryRepository().(*memoryRepository)
	identity := QueueSessionIdentity{TaskID: "task-workspace-rotation", SessionID: "session-workspace-rotation", SessionIncarnationID: "incarnation-1"}
	seedQueueSessionIdentity(t, repo, identity)
	seedCensusExecution(t, repo, identity)
	inner := repo.censusAuthority.(*censusTestAuthority)
	inner.mu.Lock()
	inner.current[identity.SessionID] = censusTestOwner{identity: identity, executionID: "execution-1", workspaceID: "workspace-1"}
	inner.mu.Unlock()
	require.NoError(t, repo.InsertForSession(ctx, identity, &QueuedMessage{
		ID: "entry-a", TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "A", QueuedBy: QueuedByUser,
	}, DefaultMaxPerSession))
	staleCtx := WithCensusWorkspaceFence(ctx, "workspace-1")
	before, err := repo.ObservePendingCensus(staleCtx, identity, "execution-1", DefaultMaxPerSession)
	require.NoError(t, err)
	inner.mu.Lock()
	inner.current[identity.SessionID] = censusTestOwner{identity: identity, executionID: "execution-1", workspaceID: "workspace-2"}
	inner.mu.Unlock()
	_, err = repo.ObservePendingCensus(staleCtx, identity, "execution-1", DefaultMaxPerSession)
	require.ErrorIs(t, err, ErrSessionIdentityMismatch)
	_, err = repo.DeleteClaimedPending(staleCtx, identity, "execution-1", "entry-a", before.Entries[0].ClaimSHA256)
	require.ErrorIs(t, err, ErrSessionIdentityMismatch)
	rows, err := repo.ListBySession(ctx, identity.SessionID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
}
