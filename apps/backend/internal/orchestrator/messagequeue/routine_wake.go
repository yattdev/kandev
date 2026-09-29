package messagequeue

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
)

const routineWakeKeyVersion = "routine-wake-v1"

const (
	metadataRoutineWake            = "routine_wake"
	metadataRoutineWakeReceipts    = "routine_wake_receipts"
	metadataRoutineWakeLeaderEntry = "routine_wake_leader_entry_id"
	metadataRoutineWakeDirty       = "routine_wake_dirty_successor"
)

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

// RoutineWakeAdmissionResult describes the single row that represents this
// admission. DirtySuccessor is true only when a retained leader already owns
// delivery and this row is its one post-run successor.
type RoutineWakeAdmissionResult struct {
	Message        *QueuedMessage
	Coalesced      bool
	DirtySuccessor bool
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

func routineWakeMetadata(receipt RoutineWakeReceipt) map[string]interface{} {
	return map[string]interface{}{
		MetadataCoalesceKey:         receipt.CanonicalKey,
		MetadataLifecycleDurable:    true,
		metadataRoutineWake:         true,
		metadataRoutineWakeReceipts: []RoutineWakeReceipt{receipt},
	}
}

func isRoutineWake(msg *QueuedMessage, key string) bool {
	if msg == nil || metadataString(msg.Metadata, MetadataCoalesceKey) != key {
		return false
	}
	routine, _ := msg.Metadata[metadataRoutineWake].(bool)
	return routine
}

func routineWakeReceipts(metadata map[string]interface{}) []RoutineWakeReceipt {
	if metadata == nil {
		return nil
	}
	encoded, err := json.Marshal(metadata[metadataRoutineWakeReceipts])
	if err != nil {
		return nil
	}
	var receipts []RoutineWakeReceipt
	if err := json.Unmarshal(encoded, &receipts); err != nil {
		return nil
	}
	return receipts
}

func appendRoutineWakeReceipt(metadata map[string]interface{}, receipt RoutineWakeReceipt) map[string]interface{} {
	updated := copyMessageMetadata(metadata, 1)
	receipts := routineWakeReceipts(updated)
	for _, existing := range receipts {
		if existing.SourceID == receipt.SourceID {
			return updated
		}
	}
	updated[metadataRoutineWakeReceipts] = append(receipts, receipt)
	return updated
}
