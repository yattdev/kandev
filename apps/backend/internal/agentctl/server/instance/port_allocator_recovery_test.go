package instance

import "testing"

// @covers AC-PLATFORM-AGENTCTL-INSTANCE-STOP-001.5
func TestPortAllocatorReusesOwnerReservation(t *testing.T) {
	allocator := NewPortAllocator(41001, 41002)

	first, err := allocator.Allocate("instance-a")
	if err != nil {
		t.Fatalf("allocate first reservation: %v", err)
	}
	second, err := allocator.Allocate("instance-a")
	if err != nil {
		t.Fatalf("allocate duplicate reservation: %v", err)
	}

	if second != first {
		t.Fatalf("duplicate owner reservation = %+v, want %+v", second, first)
	}
}
