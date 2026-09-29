package messagequeue

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/kandev/kandev/internal/common/logger"
	"github.com/stretchr/testify/require"
)

func TestAdmitRoutineWakeForSessionPendingMergeParity(t *testing.T) {
	for _, factory := range []struct {
		name string
		new  func(*testing.T) Repository
	}{
		{name: "memory", new: func(*testing.T) Repository { return NewMemoryRepository() }},
		{name: "sqlite", new: newTestSQLiteRepo},
		{name: "postgres", new: newTestPostgresRepo},
	} {
		t.Run(factory.name, func(t *testing.T) {
			ctx := context.Background()
			repo := factory.new(t)
			identity := QueueSessionIdentity{TaskID: "task", SessionID: "session", SessionIncarnationID: "incarnation"}
			seedQueueSessionIdentity(t, repo, identity)
			queue := NewService(repo, DefaultMaxPerSession, logger.Default())

			first, err := queue.AdmitRoutineWakeForSession(ctx, identity, routineWakeEnvelope("source-a"), "wake")
			require.NoError(t, err)
			second, err := queue.AdmitRoutineWakeForSession(ctx, identity, routineWakeEnvelope("source-b"), "wake")
			require.NoError(t, err)
			require.True(t, second.Coalesced)
			require.Equal(t, first.Message.ID, second.Message.ID)
			require.Len(t, routineWakeReceipts(second.Message.Metadata), 2)
			entries, err := repo.ListBySession(ctx, identity.SessionID)
			require.NoError(t, err)
			require.Len(t, entries, 1)
		})
	}
}

func TestAdmitRoutineWakeForSessionConcurrentBurst(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryRepository()
	identity := QueueSessionIdentity{TaskID: "task", SessionID: "session", SessionIncarnationID: "incarnation"}
	seedQueueSessionIdentity(t, repo, identity)
	queue := NewService(repo, DefaultMaxPerSession, logger.Default())

	var group sync.WaitGroup
	errors := make(chan error, 16)
	for index := 0; index < cap(errors); index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			_, err := queue.AdmitRoutineWakeForSession(ctx, identity, routineWakeEnvelope(fmt.Sprintf("source-%d", index)), "wake")
			errors <- err
		}(index)
	}
	group.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	entries, err := repo.ListBySession(ctx, identity.SessionID)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Len(t, routineWakeReceipts(entries[0].Metadata), cap(errors))
}

func TestAdmitRoutineWakeForSessionKeepsOneDirtySuccessorParity(t *testing.T) {
	for _, factory := range []struct {
		name string
		new  func(*testing.T) Repository
	}{
		{name: "memory", new: func(*testing.T) Repository { return NewMemoryRepository() }},
		{name: "sqlite", new: newTestSQLiteRepo},
		{name: "postgres", new: newTestPostgresRepo},
	} {
		t.Run(factory.name, func(t *testing.T) {
			ctx := context.Background()
			repo := factory.new(t)
			identity := QueueSessionIdentity{TaskID: "task", SessionID: "session", SessionIncarnationID: "incarnation"}
			seedQueueSessionIdentity(t, repo, identity)
			queue := NewService(repo, DefaultMaxPerSession, logger.Default())

			leader, err := queue.AdmitRoutineWakeForSession(ctx, identity, routineWakeEnvelope("source-a"), "wake")
			require.NoError(t, err)
			reserved, _, _, err := queue.ReserveQueuedForDeliveryWithAutoRunForSession(ctx, identity)
			require.NoError(t, err)
			require.Equal(t, leader.Message.ID, reserved.ID)
			first, err := queue.AdmitRoutineWakeForSession(ctx, identity, routineWakeEnvelope("source-b"), "wake")
			require.NoError(t, err)
			require.True(t, first.DirtySuccessor)
			second, err := queue.AdmitRoutineWakeForSession(ctx, identity, routineWakeEnvelope("source-c"), "wake")
			require.NoError(t, err)
			require.True(t, second.Coalesced)
			require.Equal(t, first.Message.ID, second.Message.ID)
			require.Len(t, routineWakeReceipts(second.Message.Metadata), 2)
			require.NoError(t, queue.AcknowledgeQueuedForSession(ctx, identity, reserved))
			entries, err := repo.ListBySession(ctx, identity.SessionID)
			require.NoError(t, err)
			require.Len(t, entries, 1)
			require.Equal(t, first.Message.ID, entries[0].ID)
		})
	}
}

func routineWakeEnvelope(source string) RoutineWakeEnvelope {
	return RoutineWakeEnvelope{
		WorkspaceID: "workspace", RoutineType: "wake", RoutineName: "cycle",
		PolicyGeneration: "policy-7", ScopeGeneration: "scope-9", SourceID: source,
	}
}
