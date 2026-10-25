package messagequeue

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/kandev/kandev/internal/common/logger"
	"github.com/stretchr/testify/require"
)

func newCensusMemoryRepository(t *testing.T) (*memoryRepository, QueueSessionIdentity) {
	t.Helper()
	repo := NewMemoryRepository().(*memoryRepository)
	identity, err := repo.ResolveSessionIdentity(context.Background(), "task", "session")
	require.NoError(t, err)
	repo.authority = func(_ context.Context, taskID, sessionID string) (QueueSessionIdentity, error) {
		if taskID != identity.TaskID || sessionID != identity.SessionID {
			return QueueSessionIdentity{}, ErrSessionIdentityMismatch
		}
		return identity, nil
	}
	return repo, identity
}

// @covers AC-TASKS-ACTIVE-QUEUE-CENSUS-001.1
func TestActiveQueueCensusProjection(t *testing.T) {
	ctx := context.Background()
	repo, identity := newCensusMemoryRepository(t)
	require.NoError(t, repo.InsertForSession(ctx, identity, &QueuedMessage{
		ID: "one", TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "first",
	}, DefaultMaxPerSession))
	service := NewService(repo, DefaultMaxPerSession, logger.Default())

	proof := ActiveQueueCensusProof{Identity: identity, ExecutionID: "execution", WorkspaceID: "workspace"}
	result, err := service.GetActiveQueueCensus(ctx, proof)
	require.NoError(t, err)
	require.Equal(t, 1, result.Count)
	require.True(t, result.Complete)
	require.Equal(t, DefaultMaxPerSession, result.Capacity)
	sum := sha256.Sum256([]byte("first"))
	require.Equal(t, []ActiveQueueCensusEntry{{
		ID: "one", Ordinal: 1, ContentSHA256: hex.EncodeToString(sum[:]), ContentBytes: 5,
	}}, result.Entries)
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "first")
	require.Empty(t, repo.statusGeneration)
}

// @covers AC-TASKS-ACTIVE-QUEUE-CENSUS-001.2 AC-TASKS-ACTIVE-QUEUE-CENSUS-001.7
func TestActiveQueueCensusMemoryRequiresAuthorityCallback(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryRepository()
	identity, err := repo.ResolveSessionIdentity(ctx, "task", "session")
	require.NoError(t, err)
	result, err := NewService(repo, 0, logger.Default()).GetActiveQueueCensus(ctx, ActiveQueueCensusProof{
		Identity: identity, ExecutionID: "execution", WorkspaceID: "workspace",
	})
	require.ErrorIs(t, err, ErrSessionIdentityMismatch)
	require.Nil(t, result)
}

// @covers AC-TASKS-ACTIVE-QUEUE-CENSUS-001.5 AC-TASKS-ACTIVE-QUEUE-CENSUS-001.7
func TestActiveQueueCensusMemoryUsesStoredEmptyMetadataSize(t *testing.T) {
	ctx := context.Background()
	repo, identity := newCensusMemoryRepository(t)
	require.NoError(t, repo.InsertForSession(ctx, identity, &QueuedMessage{
		ID: "entry", TaskID: "task", SessionID: "session", Content: strings.Repeat("x", maxCensusBytes-2),
	}, 0))
	result, err := NewService(repo, 0, logger.Default()).GetActiveQueueCensus(ctx, ActiveQueueCensusProof{
		Identity: identity, ExecutionID: "execution", WorkspaceID: "workspace",
	})
	require.NoError(t, err)
	require.Equal(t, 1, result.Count)
}

// @covers AC-TASKS-ACTIVE-QUEUE-CENSUS-001.4 AC-TASKS-ACTIVE-QUEUE-CENSUS-001.5
func TestActiveQueueCensusRevisionAndLimits(t *testing.T) {
	ctx := context.Background()
	repo, identity := newCensusMemoryRepository(t)
	for i := range 128 {
		require.NoError(t, repo.InsertForSession(ctx, identity, &QueuedMessage{
			ID: fmt.Sprintf("entry-%03d", i), TaskID: "task", SessionID: "session", Content: "é", QueuedBy: QueuedByUser,
		}, 0))
	}
	service := NewService(repo, 0, logger.Default())
	proof := ActiveQueueCensusProof{Identity: identity, ExecutionID: "execution", WorkspaceID: "workspace"}
	first, err := service.GetActiveQueueCensus(ctx, proof)
	require.NoError(t, err)
	require.Equal(t, 128, first.Count)
	require.Equal(t, 2, first.Entries[0].ContentBytes)
	require.Equal(t, 0, first.Capacity)
	again, err := service.GetActiveQueueCensus(ctx, proof)
	require.NoError(t, err)
	require.Equal(t, first.SnapshotRevisionSHA256, again.SnapshotRevisionSHA256)

	require.NoError(t, repo.UpdateContent(ctx, "session", "entry-000", "changed", nil, QueuedByUser))
	changed, err := service.GetActiveQueueCensus(ctx, proof)
	require.NoError(t, err)
	require.NotEqual(t, first.SnapshotRevisionSHA256, changed.SnapshotRevisionSHA256)
	service.SetMaxPerSession(1)
	capacityChanged, err := service.GetActiveQueueCensus(ctx, proof)
	require.NoError(t, err)
	require.NotEqual(t, changed.SnapshotRevisionSHA256, capacityChanged.SnapshotRevisionSHA256)

	require.NoError(t, repo.InsertForSession(ctx, identity, &QueuedMessage{
		ID: "extra", TaskID: "task", SessionID: "session", Content: "extra",
	}, 0))
	partial, err := service.GetActiveQueueCensus(ctx, proof)
	require.ErrorIs(t, err, ErrCensusTooLarge)
	require.Nil(t, partial)
}

// @covers AC-TASKS-ACTIVE-QUEUE-CENSUS-001.5
func TestActiveQueueCensusRejectsOversizedContentAndMetadata(t *testing.T) {
	for _, tc := range []struct {
		name     string
		content  string
		metadata map[string]interface{}
	}{
		{name: "content", content: strings.Repeat("x", maxCensusBytes+1)},
		{name: "metadata", content: "ok", metadata: map[string]interface{}{"large": strings.Repeat("x", maxCensusMetadataBytes+1)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			repo, identity := newCensusMemoryRepository(t)
			require.NoError(t, repo.InsertForSession(ctx, identity, &QueuedMessage{
				ID: "entry", TaskID: "task", SessionID: "session", Content: tc.content, Metadata: tc.metadata,
			}, 0))
			service := NewService(repo, 0, logger.Default())
			result, err := service.GetActiveQueueCensus(ctx, ActiveQueueCensusProof{
				Identity: identity, ExecutionID: "execution", WorkspaceID: "workspace",
			})
			require.ErrorIs(t, err, ErrCensusTooLarge)
			require.Nil(t, result)
		})
	}
}

// @covers AC-TASKS-ACTIVE-QUEUE-CENSUS-001.5
func TestActiveQueueCensusAcceptsExactMetadataLimit(t *testing.T) {
	ctx := context.Background()
	repo, identity := newCensusMemoryRepository(t)
	metadata := map[string]interface{}{"a": strings.Repeat("x", maxCensusMetadataBytes-8)}
	encoded, err := json.Marshal(metadata)
	require.NoError(t, err)
	require.Len(t, encoded, maxCensusMetadataBytes)
	require.NoError(t, repo.InsertForSession(ctx, identity, &QueuedMessage{
		ID: "entry", TaskID: "task", SessionID: "session", Content: "ok", Metadata: metadata,
	}, 0))
	service := NewService(repo, 0, logger.Default())
	result, err := service.GetActiveQueueCensus(ctx, ActiveQueueCensusProof{
		Identity: identity, ExecutionID: "execution", WorkspaceID: "workspace",
	})
	require.NoError(t, err)
	require.Equal(t, 1, result.Count)
}

// @covers AC-TASKS-ACTIVE-QUEUE-CENSUS-001.4
func TestActiveQueueCensusMemoryOrdersByPositionAndID(t *testing.T) {
	ctx := context.Background()
	repo, identity := newCensusMemoryRepository(t)
	repo.entries["session"] = []*QueuedMessage{
		{ID: "later", TaskID: "task", SessionID: "session", Position: 2, Content: "later"},
		{ID: "tie-z", TaskID: "task", SessionID: "session", Position: 1, Content: "z"},
		{ID: "tie-a", TaskID: "task", SessionID: "session", Position: 1, Content: "a"},
	}
	service := NewService(repo, 0, logger.Default())
	result, err := service.GetActiveQueueCensus(ctx, ActiveQueueCensusProof{
		Identity: identity, ExecutionID: "execution", WorkspaceID: "workspace",
	})
	require.NoError(t, err)
	require.Equal(t, []string{"tie-a", "tie-z", "later"}, []string{
		result.Entries[0].ID, result.Entries[1].ID, result.Entries[2].ID,
	})
}

// @covers AC-TASKS-ACTIVE-QUEUE-CENSUS-001.5 AC-TASKS-ACTIVE-QUEUE-CENSUS-001.7
func TestActiveQueueCensusRejectsInvalidHiddenRowAndExaminationOverflow(t *testing.T) {
	ctx := context.Background()
	repo, identity := newCensusMemoryRepository(t)
	proof := ActiveQueueCensusProof{Identity: identity, ExecutionID: "execution", WorkspaceID: "workspace"}
	service := NewService(repo, 0, logger.Default())
	row := &QueuedMessage{ID: strings.Repeat("x", maxCensusIDBytes+1), TaskID: "task", SessionID: "session", Position: 1,
		Metadata: map[string]interface{}{MetadataLifecycleReserved: true}}
	repo.entries["session"] = []*QueuedMessage{row}
	result, err := service.GetActiveQueueCensus(ctx, proof)
	require.ErrorIs(t, err, ErrCensusTooLarge)
	require.Nil(t, result)

	repo.entries["session"] = make([]*QueuedMessage, maxCensusExamined+1)
	for i := range repo.entries["session"] {
		repo.entries["session"][i] = &QueuedMessage{
			ID: fmt.Sprintf("entry-%04d", i), TaskID: "task", SessionID: "session", Position: int64(i + 1),
		}
	}
	result, err = service.GetActiveQueueCensus(ctx, proof)
	require.ErrorIs(t, err, ErrCensusTooLarge)
	require.Nil(t, result)
}

type changingCensusCapacityRepository struct {
	Repository
	service *Service
}

func (r *changingCensusCapacityRepository) ActiveQueueCensus(_ context.Context, proof ActiveQueueCensusProof, capacity int) (*ActiveQueueCensus, error) {
	r.service.SetMaxPerSession(capacity + 1)
	return newActiveQueueCensus(proof.Identity, capacity, nil)
}

// @covers AC-TASKS-ACTIVE-QUEUE-CENSUS-001.4
func TestActiveQueueCensusRejectsConcurrentCapacityChange(t *testing.T) {
	repo := &changingCensusCapacityRepository{}
	service := NewService(repo, 1, logger.Default())
	repo.service = service
	result, err := service.GetActiveQueueCensus(context.Background(), ActiveQueueCensusProof{
		Identity:    QueueSessionIdentity{TaskID: "task", SessionID: "session", SessionIncarnationID: "incarnation"},
		ExecutionID: "execution", WorkspaceID: "workspace",
	})
	require.Error(t, err)
	require.Nil(t, result)
}

// @covers AC-TASKS-ACTIVE-QUEUE-CENSUS-001.4
func TestActiveQueueCensusRejectsAuthorityChangeAcrossSnapshot(t *testing.T) {
	ctx := context.Background()
	repo, identity := newCensusMemoryRepository(t)
	require.NoError(t, repo.InsertForSession(ctx, identity, &QueuedMessage{
		ID: "entry", TaskID: "task", SessionID: "session", Content: "private",
	}, 0))
	calls := 0
	repo.authority = func(context.Context, string, string) (QueueSessionIdentity, error) {
		calls++
		if calls == 1 {
			return identity, nil
		}
		return QueueSessionIdentity{}, nil
	}
	service := NewService(repo, 0, logger.Default())
	result, err := service.GetActiveQueueCensus(ctx, ActiveQueueCensusProof{
		Identity: identity, ExecutionID: "execution", WorkspaceID: "workspace",
	})
	require.ErrorIs(t, err, ErrSessionIdentityMismatch)
	require.Nil(t, result)
	require.Equal(t, 2, calls)
}
