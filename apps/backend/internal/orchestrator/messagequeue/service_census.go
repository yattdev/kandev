package messagequeue

import "context"

type censusWorkspaceFenceKey struct{}
type censusOwnerFenceKey struct{}

// WithCensusWorkspaceFence carries the verified original workspace into the
// claimed effect so a concurrent task transfer cannot retarget the stream.
func WithCensusWorkspaceFence(ctx context.Context, workspaceID string) context.Context {
	return context.WithValue(ctx, censusWorkspaceFenceKey{}, workspaceID)
}

func censusWorkspaceFence(ctx context.Context) string {
	workspaceID, _ := ctx.Value(censusWorkspaceFenceKey{}).(string)
	return workspaceID
}

// WithCensusOwnerFence carries the launch-bound owner to the final repository check.
func WithCensusOwnerFence(ctx context.Context, ownerID string) context.Context {
	return context.WithValue(ctx, censusOwnerFenceKey{}, ownerID)
}

func censusOwnerFence(ctx context.Context) string {
	ownerID, _ := ctx.Value(censusOwnerFenceKey{}).(string)
	return ownerID
}

type censusRepository interface {
	ObservePendingCensus(context.Context, QueueSessionIdentity, string, int) (PendingCensus, error)
	DeleteClaimedPending(context.Context, QueueSessionIdentity, string, string, string) (*QueueRemovalResult, error)
}

// ObservePendingCensus samples this serving process's configured capacity once
// and delegates the authority-bound, read-only snapshot to the repository.
func (s *Service) ObservePendingCensus(ctx context.Context, identity QueueSessionIdentity, executionID string) (PendingCensus, error) {
	repo, ok := s.repo.(censusRepository)
	if !ok {
		return PendingCensus{}, ErrQueueAdmissionUnavailable
	}
	capacity := int(s.maxPerSession.Load())
	return repo.ObservePendingCensus(ctx, identity, executionID, capacity)
}

// RemoveClaimedPending serializes the conditional effect with other local
// session admissions while the repository enforces the durable revision and
// execution fence inside its own transaction.
func (s *Service) RemoveClaimedPending(ctx context.Context, identity QueueSessionIdentity, executionID, entryID, claim string) (*QueueRemovalResult, error) {
	repo, ok := s.repo.(censusRepository)
	if !ok {
		return nil, ErrQueueAdmissionUnavailable
	}
	var result *QueueRemovalResult
	err := s.WithSessionAdmission(ctx, identity.SessionID, func(admittedCtx context.Context) error {
		var effectErr error
		result, effectErr = repo.DeleteClaimedPending(admittedCtx, identity, executionID, entryID, claim)
		return effectErr
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
