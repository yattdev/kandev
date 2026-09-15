// Package instance provides utilities for managing multi-agent instances,
// including dynamic port allocation for agent services.
package instance

import (
	"fmt"
	"sync"
)

// PortAllocator manages dynamic port allocation for multi-agent instances.
// It tracks which ports are in use and provides thread-safe allocation
// and release of ports within a configured range.
type PortAllocator struct {
	basePort               int
	maxPort                int
	allocated              map[int]PortLease
	byOwner                map[string]PortLease
	unavailable            map[int]struct{}
	nextGeneration         uint64
	allocationSuccess      uint64
	allocationExhausted    uint64
	allocationAlreadyOwned uint64
	releaseReleased        uint64
	releaseAlreadyReleased uint64
	releaseOwnerMismatch   uint64
	mu                     sync.Mutex
}

// PortLease is the immutable authority to release a reserved port. A port
// number alone is deliberately insufficient: a late cleanup must not free a
// successor that reused the same numeric port.
type PortLease struct {
	Port       int
	Owner      string
	Generation uint64
}

// ReleaseOutcome describes whether a lease release changed allocator state.
type ReleaseOutcome string

const (
	ReleaseOutcomeReleased        ReleaseOutcome = "released"
	ReleaseOutcomeAlreadyReleased ReleaseOutcome = "already_released"
	ReleaseOutcomeOwnerMismatch   ReleaseOutcome = "owner_mismatch"
)

// portPoolAllocationSnapshot is a point-in-time allocator view for control
// server diagnostics. It intentionally contains no instance, session, or port
// identifiers so it can be safely aggregated by callers.
type portPoolAllocationSnapshot struct {
	Capacity               int    `json:"capacity"`
	Reserved               int    `json:"reserved"`
	AllocationSuccess      uint64 `json:"allocation_success"`
	AllocationExhausted    uint64 `json:"allocation_exhausted"`
	AllocationAlreadyOwned uint64 `json:"allocation_already_owned"`
	ReleaseReleased        uint64 `json:"release_released"`
	ReleaseAlreadyReleased uint64 `json:"release_already_released"`
	ReleaseOwnerMismatch   uint64 `json:"release_owner_mismatch"`
}

// NewPortAllocator creates a new PortAllocator that manages ports
// in the range [basePort, maxPort].
func NewPortAllocator(basePort, maxPort int) *PortAllocator {
	return &PortAllocator{
		basePort:    basePort,
		maxPort:     maxPort,
		allocated:   make(map[int]PortLease),
		byOwner:     make(map[string]PortLease),
		unavailable: make(map[int]struct{}),
	}
}

// Allocate finds and reserves an available port for the given instance ID.
// It performs a linear search starting from basePort up to maxPort.
// Returns the allocated port number, or an error if no ports are available.
func (p *PortAllocator) Allocate(instanceID string) (PortLease, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if existing, ok := p.byOwner[instanceID]; ok {
		p.allocationAlreadyOwned++
		return existing, nil
	}

	for port := p.basePort; port <= p.maxPort; port++ {
		if _, blocked := p.unavailable[port]; blocked {
			continue
		}
		if _, exists := p.allocated[port]; !exists {
			p.nextGeneration++
			lease := PortLease{Port: port, Owner: instanceID, Generation: p.nextGeneration}
			p.allocated[port] = lease
			p.byOwner[instanceID] = lease
			p.allocationSuccess++
			return lease, nil
		}
	}

	p.allocationExhausted++
	return PortLease{}, fmt.Errorf("no available ports in range [%d, %d]", p.basePort, p.maxPort)
}

// Release frees exactly the lease that was reserved. It is idempotent for an
// already-released lease and leaves a successor reservation intact.
func (p *PortAllocator) Release(lease PortLease) ReleaseOutcome {
	p.mu.Lock()
	defer p.mu.Unlock()

	current, ok := p.allocated[lease.Port]
	if !ok {
		p.releaseAlreadyReleased++
		return ReleaseOutcomeAlreadyReleased
	}
	if current.Owner != lease.Owner || current.Generation != lease.Generation {
		p.releaseOwnerMismatch++
		return ReleaseOutcomeOwnerMismatch
	}
	delete(p.allocated, lease.Port)
	delete(p.byOwner, lease.Owner)
	p.releaseReleased++
	return ReleaseOutcomeReleased
}

// MarkUnavailable prevents a port from being allocated again in this process.
func (p *PortAllocator) MarkUnavailable(lease PortLease) ReleaseOutcome {
	p.mu.Lock()
	defer p.mu.Unlock()

	current, ok := p.allocated[lease.Port]
	if !ok {
		p.releaseAlreadyReleased++
		return ReleaseOutcomeAlreadyReleased
	}
	if current.Owner != lease.Owner || current.Generation != lease.Generation {
		p.releaseOwnerMismatch++
		return ReleaseOutcomeOwnerMismatch
	}
	p.unavailable[lease.Port] = struct{}{}
	delete(p.allocated, lease.Port)
	delete(p.byOwner, lease.Owner)
	p.releaseReleased++
	return ReleaseOutcomeReleased
}

func (p *PortAllocator) Reserved() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.allocated)
}

func (p *PortAllocator) Snapshot() portPoolAllocationSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	return portPoolAllocationSnapshot{
		Capacity:               p.maxPort - p.basePort + 1,
		Reserved:               len(p.allocated),
		AllocationSuccess:      p.allocationSuccess,
		AllocationExhausted:    p.allocationExhausted,
		AllocationAlreadyOwned: p.allocationAlreadyOwned,
		ReleaseReleased:        p.releaseReleased,
		ReleaseAlreadyReleased: p.releaseAlreadyReleased,
		ReleaseOwnerMismatch:   p.releaseOwnerMismatch,
	}
}
