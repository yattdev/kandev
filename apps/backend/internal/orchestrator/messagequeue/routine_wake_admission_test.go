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

func TestAdmitRoutineWakeForSessionReleaseSettlesLeaderParity(t *testing.T) {
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
			_, err := queue.AdmitRoutineWakeForSession(ctx, identity, routineWakeEnvelope("source-a"), "wake")
			require.NoError(t, err)
			leader, _, _, err := queue.ReserveQueuedForDeliveryWithAutoRunForSession(ctx, identity)
			require.NoError(t, err)
			successor, err := queue.AdmitRoutineWakeForSession(ctx, identity, routineWakeEnvelope("source-b"), "wake")
			require.NoError(t, err)
			require.NoError(t, queue.ReleaseQueuedDeliveryForSession(ctx, identity, leader))
			entries, err := repo.ListBySession(ctx, identity.SessionID)
			require.NoError(t, err)
			require.Len(t, entries, 1)
			require.Equal(t, successor.Message.ID, entries[0].ID)
		})
	}
}

func TestAdmitRoutineWakeForSessionRestartedServiceSettlesLeaderParity(t *testing.T) {
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

			_, err := queue.AdmitRoutineWakeForSession(ctx, identity, routineWakeEnvelope("source-a"), "wake")
			require.NoError(t, err)
			leader, _, _, err := queue.ReserveQueuedForDeliveryWithAutoRunForSession(ctx, identity)
			require.NoError(t, err)
			successor, err := queue.AdmitRoutineWakeForSession(ctx, identity, routineWakeEnvelope("source-b"), "wake")
			require.NoError(t, err)

			restarted := NewService(repo, DefaultMaxPerSession, logger.Default())
			require.NoError(t, restarted.ReleaseQueuedDeliveryForSession(ctx, identity, leader))
			entries, err := repo.ListBySession(ctx, identity.SessionID)
			require.NoError(t, err)
			require.Len(t, entries, 1)
			require.Equal(t, successor.Message.ID, entries[0].ID)
		})
	}
}

func TestAdmitRoutineWakeForSessionStaleIncarnationFencesLeaderParity(t *testing.T) {
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
			first := QueueSessionIdentity{TaskID: "task", SessionID: "session", SessionIncarnationID: "incarnation-a"}
			seedQueueSessionIdentity(t, repo, first)
			queue := NewService(repo, DefaultMaxPerSession, logger.Default())

			_, err := queue.AdmitRoutineWakeForSession(ctx, first, routineWakeEnvelope("source-a"), "wake")
			require.NoError(t, err)
			leader, _, _, err := queue.ReserveQueuedForDeliveryWithAutoRunForSession(ctx, first)
			require.NoError(t, err)
			successor, err := queue.AdmitRoutineWakeForSession(ctx, first, routineWakeEnvelope("source-b"), "wake")
			require.NoError(t, err)

			replacement := first
			replacement.SessionIncarnationID = "incarnation-b"
			seedQueueSessionIdentity(t, repo, replacement)
			require.ErrorIs(t, queue.ReleaseQueuedDeliveryForSession(ctx, first, leader), ErrSessionIdentityMismatch)
			reserved, _, _, err := queue.ReserveQueuedForDeliveryWithAutoRunForSession(ctx, replacement)
			require.NoError(t, err)
			require.NotNil(t, reserved)
			require.Equal(t, successor.Message.ID, reserved.ID)
			entries, err := repo.ListBySession(ctx, replacement.SessionID)
			require.NoError(t, err)
			require.Len(t, entries, 1)
			require.Equal(t, successor.Message.ID, entries[0].ID)
		})
	}
}

func TestAdmitRoutineWakeForSessionPreservesDistinctMessagesParity(t *testing.T) {
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
			_, err := queue.AdmitRoutineWakeForSession(ctx, identity, routineWakeEnvelope("source-a"), "wake")
			require.NoError(t, err)
			for _, queuedBy := range []string{QueuedByUser, QueuedByAgent, QueuedByWorkflow} {
				_, err := queue.QueueMessage(ctx, identity.SessionID, identity.TaskID, "distinct "+queuedBy, "", queuedBy, false, nil)
				require.NoError(t, err)
			}
			entries, err := repo.ListBySession(ctx, identity.SessionID)
			require.NoError(t, err)
			require.Len(t, entries, 4)
		})
	}
}

func routineWakeEnvelope(source string) RoutineWakeEnvelope {
	return RoutineWakeEnvelope{
		WorkspaceID: "workspace", RoutineType: "wake", RoutineName: "cycle",
		PolicyGeneration: "policy-7", ScopeGeneration: "scope-9", SourceID: source,
	}
}
