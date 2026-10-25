package messagequeue

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"
)

const (
	maxCensusVisible       = 128
	maxCensusExamined      = 1024
	maxCensusBytes         = 8 << 20
	maxCensusMetadataBytes = 32 << 10
	maxCensusIDBytes       = 256
	maxCensusResultBytes   = 65536
)

var ErrCensusTooLarge = errors.New("queue census resource limit exceeded")
var ErrCensusChanged = errors.New("queue census changed during read")

// ActiveQueueCensusProof is populated only after the orchestrator has verified
// the current trusted stream execution and exact task/session owner.
type ActiveQueueCensusProof struct {
	Identity    QueueSessionIdentity
	ExecutionID string
	WorkspaceID string
}

type ActiveQueueCensusEntry struct {
	ID            string `json:"id"`
	Ordinal       int    `json:"ordinal"`
	ContentSHA256 string `json:"content_sha256"`
	ContentBytes  int    `json:"content_bytes"`
}

type ActiveQueueCensus struct {
	Version                int                      `json:"version"`
	TaskID                 string                   `json:"task_id"`
	SessionID              string                   `json:"session_id"`
	SessionIncarnationID   string                   `json:"session_incarnation_id"`
	SnapshotRevisionSHA256 string                   `json:"snapshot_revision_sha256"`
	Complete               bool                     `json:"complete"`
	Count                  int                      `json:"count"`
	Capacity               int                      `json:"capacity"`
	Entries                []ActiveQueueCensusEntry `json:"entries"`
}

func (s *Service) GetActiveQueueCensus(ctx context.Context, proof ActiveQueueCensusProof) (*ActiveQueueCensus, error) {
	identity := proof.Identity
	if s == nil || s.repo == nil || identity.TaskID == "" || identity.SessionID == "" || identity.SessionIncarnationID == "" || proof.ExecutionID == "" || proof.WorkspaceID == "" {
		return nil, ErrSessionIdentityMismatch
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	capacity := s.MaxPerSession()
	result, err := s.repo.ActiveQueueCensus(ctx, proof, capacity)
	if err != nil {
		return nil, err
	}
	if s.MaxPerSession() != capacity {
		return nil, ErrCensusChanged
	}
	return result, nil
}

func newActiveQueueCensus(identity QueueSessionIdentity, capacity int, entries []ActiveQueueCensusEntry) (*ActiveQueueCensus, error) {
	if entries == nil {
		entries = []ActiveQueueCensusEntry{}
	}
	result := &ActiveQueueCensus{Version: 1, TaskID: identity.TaskID, SessionID: identity.SessionID,
		SessionIncarnationID: identity.SessionIncarnationID, Complete: true, Count: len(entries),
		Capacity: capacity, Entries: entries}
	canonical, err := json.Marshal(struct {
		Domain      string                   `json:"domain"`
		Version     int                      `json:"version"`
		TaskID      string                   `json:"task_id"`
		SessionID   string                   `json:"session_id"`
		Incarnation string                   `json:"incarnation"`
		Capacity    int                      `json:"capacity"`
		Entries     []ActiveQueueCensusEntry `json:"entries"`
	}{"active-queue-census/v1", 1, identity.TaskID, identity.SessionID, identity.SessionIncarnationID, capacity, entries})
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(canonical)
	result.SnapshotRevisionSHA256 = hex.EncodeToString(sum[:])
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	if len(encoded) > maxCensusResultBytes {
		return nil, ErrCensusTooLarge
	}
	return result, nil
}

func censusEntry(id, content string, ordinal int) (ActiveQueueCensusEntry, error) {
	if id == "" || !utf8.ValidString(id) || len(id) > maxCensusIDBytes {
		return ActiveQueueCensusEntry{}, ErrCensusTooLarge
	}
	if !utf8.ValidString(content) {
		return ActiveQueueCensusEntry{}, fmt.Errorf("invalid queue content encoding")
	}
	sum := sha256.Sum256([]byte(content))
	return ActiveQueueCensusEntry{ID: id, Ordinal: ordinal, ContentSHA256: hex.EncodeToString(sum[:]), ContentBytes: len(content)}, nil
}
