package messagequeue

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/common/logger"
	"github.com/stretchr/testify/require"
)

func TestRemoveEntryForSessionWithClaimParity(t *testing.T) {
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
			queue := NewService(repo, DefaultMaxPerSession, logger.Default())
			identity := QueueSessionIdentity{TaskID: "task", SessionID: "session", SessionIncarnationID: "incarnation"}
			seedQueueSessionIdentity(t, repo, identity)
			entry, err := queue.QueueMessageWithMetadataForSession(ctx, identity, "body", "", QueuedByAgent, false, nil, nil)
			require.NoError(t, err)
			removed, err := queue.RemoveEntryForSessionWithClaim(ctx, identity, entry.ID, QueueEntryClaim(entry))
			require.NoError(t, err)
			require.Len(t, removed.Removed, 1)
			entry, err = queue.QueueMessageWithMetadataForSession(ctx, identity, "body", "", QueuedByAgent, false, nil, nil)
			require.NoError(t, err)
			_, err = queue.RemoveEntryForSessionWithClaim(ctx, identity, entry.ID, "stale")
			require.ErrorIs(t, err, ErrQueueEntryClaimChanged)
		})
	}
}
