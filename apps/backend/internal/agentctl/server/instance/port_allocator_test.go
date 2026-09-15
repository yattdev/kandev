package instance

import (
	"fmt"
	"sync"
	"testing"
)

// @covers AC-PLATFORM-AGENTCTL-INSTANCE-STOP-001.2
func TestPortAllocatorAllocateIsIdempotentForOwner(t *testing.T) {
	allocator := NewPortAllocator(41001, 41002)

	first, err := allocator.Allocate("instance-a")
	if err != nil {
		t.Fatalf("first Allocate: %v", err)
	}
	second, err := allocator.Allocate("instance-a")
	if err != nil {
		t.Fatalf("second Allocate: %v", err)
	}

	if second != first {
		t.Fatalf("second allocation = %#v, want existing owner lease %#v", second, first)
	}
}

// @covers AC-PLATFORM-AGENTCTL-INSTANCE-STOP-001.3
func TestPortAllocatorRejectsStaleReleaseAfterPortReuse(t *testing.T) {
	allocator := NewPortAllocator(41001, 41001)

	first, err := allocator.Allocate("instance-a")
	if err != nil {
		t.Fatalf("first Allocate: %v", err)
	}
	if got := allocator.Release(first); got != ReleaseOutcomeReleased {
		t.Fatalf("first Release = %q, want %q", got, ReleaseOutcomeReleased)
	}
	successor, err := allocator.Allocate("instance-b")
	if err != nil {
		t.Fatalf("successor Allocate: %v", err)
	}

	if got := allocator.Release(first); got != ReleaseOutcomeOwnerMismatch {
		t.Fatalf("stale Release = %q, want %q", got, ReleaseOutcomeOwnerMismatch)
	}
	if got := allocator.Reserved(); got != 1 {
		t.Fatalf("reserved after stale release = %d, want successor reservation retained", got)
	}
	if got := allocator.Release(successor); got != ReleaseOutcomeReleased {
		t.Fatalf("successor Release = %q, want %q", got, ReleaseOutcomeReleased)
	}
	snapshot := allocator.Snapshot()
	if snapshot.ReleaseOwnerMismatch != 1 || snapshot.ReleaseReleased != 2 {
		t.Fatalf("release counters = %+v, want one mismatch and two releases", snapshot)
	}
}

// @covers AC-PLATFORM-AGENTCTL-INSTANCE-STOP-001.4
func TestPortAllocatorConcurrentAllocationsUseEachPortOnce(t *testing.T) {
	const capacity = 16
	allocator := NewPortAllocator(41001, 41000+capacity)
	leases := make(chan PortLease, capacity)
	errs := make(chan error, capacity)
	var wg sync.WaitGroup
	for i := 0; i < capacity; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			lease, err := allocator.Allocate(fmt.Sprintf("instance-%d", i))
			if err != nil {
				errs <- err
				return
			}
			leases <- lease
		}(i)
	}
	wg.Wait()
	close(leases)
	close(errs)
	for err := range errs {
		t.Errorf("Allocate: %v", err)
	}

	ports := make(map[int]struct{}, capacity)
	for lease := range leases {
		if _, exists := ports[lease.Port]; exists {
			t.Errorf("duplicate live lease for port %d", lease.Port)
		}
		ports[lease.Port] = struct{}{}
	}
	if len(ports) != capacity {
		t.Fatalf("allocated ports = %d, want %d", len(ports), capacity)
	}
	if _, err := allocator.Allocate("overflow"); err == nil {
		t.Fatal("Allocate overflow succeeded after every port was reserved")
	}
}
