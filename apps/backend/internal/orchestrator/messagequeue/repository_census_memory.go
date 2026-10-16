package messagequeue

import (
	"context"
	"sort"
	"time"
)

// CensusAuthorityGuard holds the same lifecycle gate used to rotate an
// execution or session while a memory-backed census effect is admitted.
type CensusAuthorityGuard interface {
	WithinCurrentExecution(context.Context, QueueSessionIdentity, string, string, string, func() error) error
}

func NewMemoryRepositoryWithCensusGuard(guard CensusAuthorityGuard) Repository {
	repo := NewMemoryRepository().(*memoryRepository)
	repo.censusAuthority = guard
	return repo
}

// AdvanceCensusRevisionForAuthorityChange is called while the lifecycle
// authority guard holds its owner-change lock, matching census lock order.
func (r *memoryRepository) AdvanceCensusRevisionForAuthorityChange(sessionID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.canBumpCensusLocked(sessionID); err != nil {
		return err
	}
	r.bumpCensusLocked(sessionID)
	return nil
}

func (r *memoryRepository) ObservePendingCensus(ctx context.Context, identity QueueSessionIdentity, executionID string, capacity int) (PendingCensus, error) {
	if r.censusAuthority == nil {
		return PendingCensus{}, ErrQueueAdmissionUnavailable
	}
	var census PendingCensus
	err := r.censusAuthority.WithinCurrentExecution(ctx, identity, executionID, censusWorkspaceFence(ctx), censusOwnerFence(ctx), func() error {
		r.mu.Lock()
		defer r.mu.Unlock()
		var readErr error
		census, readErr = r.observePendingCensusLocked(ctx, identity, executionID, capacity)
		return readErr
	})
	if err != nil {
		return PendingCensus{}, err
	}
	return census, nil
}

func (r *memoryRepository) observePendingCensusLocked(ctx context.Context, identity QueueSessionIdentity, executionID string, capacity int) (PendingCensus, error) {
	if err := ctx.Err(); err != nil {
		return PendingCensus{}, err
	}
	if r.identities[identity.SessionID] != identity {
		return PendingCensus{}, ErrSessionIdentityMismatch
	}
	if len(r.entries[identity.SessionID]) > censusMaxStoredRows {
		return PendingCensus{}, ErrCensusResourceExhausted
	}
	census := PendingCensus{
		Version: censusSnapshotVersion, Identity: identity, OriginalExecutionID: executionID,
		Revision: r.censusRevision[identity.SessionID],
		Capacity: capacity, Entries: make([]PendingCensusEntry, 0, len(r.entries[identity.SessionID])),
	}
	ordered := append([]*QueuedMessage(nil), r.entries[identity.SessionID]...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Position == ordered[j].Position {
			return ordered[i].ID < ordered[j].ID
		}
		return ordered[i].Position < ordered[j].Position
	})
	for index, message := range ordered {
		if index%16 == 0 {
			if err := ctx.Err(); err != nil {
				return PendingCensus{}, err
			}
		}
		if message.IsReservedInFlight() {
			continue
		}
		entry, err := NewPendingCensusEntry(identity, executionID, census.Revision, *message)
		if err != nil {
			return PendingCensus{}, err
		}
		census.Entries = append(census.Entries, entry)
		if len(census.Entries) > censusMaxVisibleRows {
			return PendingCensus{}, ErrCensusResourceExhausted
		}
	}
	census.Count = len(census.Entries)
	if err := census.Finalize(); err != nil {
		return PendingCensus{}, err
	}
	return census, nil
}

func (r *memoryRepository) DeleteClaimedPending(ctx context.Context, identity QueueSessionIdentity, executionID, entryID, claim string) (*QueueRemovalResult, error) {
	if r.censusAuthority == nil {
		return nil, ErrQueueAdmissionUnavailable
	}
	var removed *QueueRemovalResult
	err := r.censusAuthority.WithinCurrentExecution(ctx, identity, executionID, censusWorkspaceFence(ctx), censusOwnerFence(ctx), func() error {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.identities[identity.SessionID] != identity {
			return ErrSessionIdentityMismatch
		}
		if !validCensusDigest(claim) {
			return ErrQueueChanged
		}
		expected, err := CensusEntryClaim(identity, executionID, entryID, r.censusRevision[identity.SessionID])
		if err != nil || claim != expected {
			return ErrQueueChanged
		}
		if lease := r.censusEditLeases[editLeaseKey{sessionID: identity.SessionID, entryID: entryID}]; lease != nil &&
			lease.ExpiresAt.After(time.Now().UTC()) {
			return ErrEditConflict
		}
		removed, err = r.deleteByIDForSessionLocked(identity.SessionID, entryID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return removed, nil
}

func (r *memoryRepository) acquireEditLease(_ context.Context, lease *QueueEditLease) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := editLeaseKey{sessionID: lease.SessionID, entryID: lease.EntryID}
	if current := r.censusEditLeases[key]; current != nil && current.ExpiresAt.After(time.Now().UTC()) {
		return ErrEditConflict
	}
	if err := r.canBumpCensusLocked(lease.SessionID); err != nil {
		return err
	}
	r.censusEditLeases[key] = cloneEditLease(lease)
	r.bumpCensusLocked(lease.SessionID)
	return nil
}

func (r *memoryRepository) renewEditLease(_ context.Context, lease *QueueEditLease) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := editLeaseKey{sessionID: lease.SessionID, entryID: lease.EntryID}
	current := r.censusEditLeases[key]
	if current == nil || current.LeaseID != lease.LeaseID || !current.ExpiresAt.After(time.Now().UTC()) {
		return ErrEditLeaseNotFound
	}
	if err := r.canBumpCensusLocked(lease.SessionID); err != nil {
		return err
	}
	r.censusEditLeases[key] = cloneEditLease(lease)
	r.bumpCensusLocked(lease.SessionID)
	return nil
}

func (r *memoryRepository) releaseEditLease(_ context.Context, sessionID, entryID, leaseID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := editLeaseKey{sessionID: sessionID, entryID: entryID}
	current := r.censusEditLeases[key]
	if current == nil || current.LeaseID != leaseID {
		return nil
	}
	if err := r.canBumpCensusLocked(sessionID); err != nil {
		return err
	}
	delete(r.censusEditLeases, key)
	r.bumpCensusLocked(sessionID)
	return nil
}
