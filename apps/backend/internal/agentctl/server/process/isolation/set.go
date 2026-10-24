package isolation

import (
	"errors"
	"sync"
)

// CaptureAllPinned captures ordered grant identities while retaining sources.
func CaptureAllPinned(paths []string) ([]Identity, *Set, error) {
	set := &Set{grants: make(map[string]*Pinned, len(paths))}
	identities := make([]Identity, 0, len(paths))
	for _, path := range paths {
		if _, exists := set.grants[path]; exists {
			_ = set.Close()
			return nil, nil, errors.New("duplicate isolation grant")
		}
		identity, pin, err := CapturePinned(path)
		if err != nil {
			_ = set.Close()
			return nil, nil, err
		}
		set.grants[path] = pin
		identities = append(identities, identity)
	}
	return identities, set, nil
}

// Set owns every descriptor retained for one instance's isolation grants.
type Set struct {
	mu     sync.Mutex
	grants map[string]*Pinned
	closed bool
}

func PinAll(identities []Identity) (*Set, error) {
	set := &Set{grants: make(map[string]*Pinned, len(identities))}
	for _, identity := range identities {
		if _, exists := set.grants[identity.Path]; exists {
			_ = set.Close()
			return nil, errors.New("duplicate isolation object")
		}
		pin, err := Pin(identity)
		if err != nil {
			_ = set.Close()
			return nil, err
		}
		set.grants[identity.Path] = pin
	}
	if err := set.Validate(); err != nil {
		_ = set.Close()
		return nil, err
	}
	return set, nil
}

// Get returns the retained object for a helper installer while the instance lives.
func (s *Set) Get(path string) (*Pinned, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, false
	}
	pin, ok := s.grants[path]
	return pin, ok
}

// Validate rechecks paths at the installation boundary without reopening grants.
func (s *Set) Validate() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("isolation objects already closed")
	}
	for _, pin := range s.grants {
		if err := pin.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// Close releases every retained descriptor, including on partial create failure.
func (s *Set) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	var errs []error
	for _, pin := range s.grants {
		errs = append(errs, pin.Close())
	}
	s.grants = nil
	return errors.Join(errs...)
}
