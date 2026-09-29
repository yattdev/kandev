package messagequeue

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

const routineWakeKeyVersion = "routine-wake-v1"

// RoutineWakeEnvelope is the scheduler-authenticated identity of one routine
// wake. Carrier task, session, and message IDs are deliberately absent so
// equivalent scheduler deliveries share one queue identity.
type RoutineWakeEnvelope struct {
	WorkspaceID      string
	RoutineType      string
	RoutineName      string
	PolicyGeneration string
	ScopeGeneration  string
	SourceID         string
}

// RoutineWakeReceipt is body-free durable metadata for an accepted source.
type RoutineWakeReceipt struct {
	CanonicalKey     string `json:"canonical_key"`
	SourceID         string `json:"source_id"`
	RoutineType      string `json:"routine_type"`
	RoutineName      string `json:"routine_name"`
	PolicyGeneration string `json:"policy_generation"`
	ScopeGeneration  string `json:"scope_generation"`
}

// CanonicalKey derives the cross-carrier routine identity from scheduler-owned
// dimensions. SourceID is a receipt identity and never changes the key.
func (e RoutineWakeEnvelope) CanonicalKey() (string, error) {
	if err := e.validateIdentity(); err != nil {
		return "", err
	}
	values := []string{
		routineWakeKeyVersion,
		e.WorkspaceID,
		e.RoutineType,
		e.RoutineName,
		e.PolicyGeneration,
		e.ScopeGeneration,
	}
	sum := sha256.Sum256([]byte(strings.Join(values, "\x00")))
	return hex.EncodeToString(sum[:]), nil
}

// Receipt returns metadata that can be persisted without the routine prompt.
func (e RoutineWakeEnvelope) Receipt() (RoutineWakeReceipt, error) {
	if strings.TrimSpace(e.SourceID) == "" {
		return RoutineWakeReceipt{}, errors.New("routine wake source id is required")
	}
	key, err := e.CanonicalKey()
	if err != nil {
		return RoutineWakeReceipt{}, err
	}
	return RoutineWakeReceipt{
		CanonicalKey: key, SourceID: e.SourceID, RoutineType: e.RoutineType,
		RoutineName: e.RoutineName, PolicyGeneration: e.PolicyGeneration,
		ScopeGeneration: e.ScopeGeneration,
	}, nil
}

func (e RoutineWakeEnvelope) validateIdentity() error {
	for _, field := range []string{
		e.WorkspaceID,
		e.RoutineType,
		e.RoutineName,
		e.PolicyGeneration,
		e.ScopeGeneration,
	} {
		if strings.TrimSpace(field) == "" {
			return errors.New("routine wake identity is incomplete")
		}
	}
	return nil
}
