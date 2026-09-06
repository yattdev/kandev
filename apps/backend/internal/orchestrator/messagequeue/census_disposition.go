package messagequeue

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"
)

// QueueDispositionStatus is the durable, per-entry result vocabulary returned
// by exact queue disposition.
type QueueDispositionStatus string

const (
	QueueDispositionRemoved  QueueDispositionStatus = "removed"
	QueueDispositionNotFound QueueDispositionStatus = "not_found"
	QueueDispositionChanged  QueueDispositionStatus = "changed"
)

// ErrInvalidQueueDisposition identifies malformed exact-disposition input.
// Repository and transaction errors are deliberately not classified as
// validation failures at the transport boundary.
var ErrInvalidQueueDisposition = errors.New("invalid queue disposition")

// QueueEntryClaim binds an immutable queue entry ID to the exact snapshot an
// authorized caller observed in a census. Claims are opaque to callers.
type QueueEntryClaim struct {
	ID    string `json:"id"`
	Claim string `json:"claim"`
}

// QueueDispositionOutcome reports whether one claimed entry was removed.
type QueueDispositionOutcome struct {
	ID     string                 `json:"id"`
	Status QueueDispositionStatus `json:"status"`
}

// QueueDispositionResult reports atomic before/after counts and every requested
// entry outcome. Counts include visible pending entries only.
type QueueDispositionResult struct {
	BeforeCount int                       `json:"before_count"`
	AfterCount  int                       `json:"after_count"`
	Outcomes    []QueueDispositionOutcome `json:"outcomes"`
}

// QueueCensusEntry is a content-free descriptor for one visible pending FIFO
// entry. The digest permits equality checks without exposing the message body.
type QueueCensusEntry struct {
	ID              string `json:"id"`
	Claim           string `json:"claim"`
	Position        int64  `json:"position"`
	QueuedAt        string `json:"queued_at"`
	QueuedBy        string `json:"queued_by"`
	Origin          string `json:"origin,omitempty"`
	SenderTaskID    string `json:"sender_task_id,omitempty"`
	RoutineWake     bool   `json:"routine_wake"`
	RoutineIdentity string `json:"routine_identity,omitempty"`
	ContentSHA256   string `json:"content_sha256"`
	ContentBytes    int    `json:"content_bytes"`
	AttachmentCount int    `json:"attachment_count"`
}

// QueueCensus is the exact FIFO snapshot returned to a session-bound caller.
type QueueCensus struct {
	Entries     []QueueCensusEntry `json:"entries"`
	BeforeCount int                `json:"before_count"`
	Max         int                `json:"max"`
	AutoRun     bool               `json:"auto_run"`
}

// QueueRecoveryEntry is the exact, task-scoped readback a replacement primary
// uses to reconstruct a retired helper session's FIFO. Arbitrary metadata is
// intentionally excluded; only stable delivery fields and safe provenance are
// exposed.
type QueueRecoveryEntry struct {
	ID               string              `json:"id"`
	SessionID        string              `json:"session_id"`
	TaskID           string              `json:"task_id"`
	Position         int64               `json:"position"`
	Content          string              `json:"content"`
	ContentSHA256    string              `json:"content_sha256"`
	ContentBytes     int                 `json:"content_bytes"`
	Model            string              `json:"model"`
	PlanMode         bool                `json:"plan_mode"`
	Attachments      []MessageAttachment `json:"attachments"`
	QueuedAt         string              `json:"queued_at"`
	QueuedBy         string              `json:"queued_by"`
	Origin           string              `json:"origin,omitempty"`
	SenderTaskID     string              `json:"sender_task_id,omitempty"`
	ReservedInFlight bool                `json:"reserved_in_flight"`
}

// RecoverySnapshot returns the complete persisted FIFO, including entries
// reserved by a predecessor that can no longer finish delivery. Authorization
// belongs to the MCP handler; this service only owns queue consistency.
func (s *Service) RecoverySnapshot(ctx context.Context, sessionID string) ([]QueueRecoveryEntry, error) {
	entries, err := s.repo.ListBySession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list queue recovery snapshot: %w", err)
	}
	return RecoveryEntries(entries), nil
}

// RecoverSessionQueue atomically moves every source row to the replacement
// session while returning the exact pre-move FIFO. This is distinct from a
// workflow session transfer: stale durable reservations are cleared so a row
// left in flight by a crashed helper becomes deliverable again.
func (s *Service) RecoverSessionQueue(
	ctx context.Context,
	sourceSessionID, destinationSessionID string,
) ([]QueueRecoveryEntry, error) {
	if sourceSessionID == "" || destinationSessionID == "" || sourceSessionID == destinationSessionID {
		return nil, fmt.Errorf("%w: distinct source and destination session ids are required", ErrInvalidQueueDisposition)
	}
	entries, err := s.repo.RecoverSessionQueue(ctx, sourceSessionID, destinationSessionID)
	if err != nil {
		return nil, fmt.Errorf("recover session queue: %w", err)
	}
	s.logger.Info("recovered queue between sessions",
		zap.String("from_session_id", sourceSessionID),
		zap.String("to_session_id", destinationSessionID),
		zap.Int("entries", len(entries)))
	return RecoveryEntries(entries), nil
}

// RecoveryEntries converts persisted queue snapshots into the guarded recovery
// response shape without exposing unfiltered metadata.
func RecoveryEntries(entries []QueuedMessage) []QueueRecoveryEntry {
	result := make([]QueueRecoveryEntry, 0, len(entries))
	for i := range entries {
		entry := &entries[i]
		digest := sha256.Sum256([]byte(entry.Content))
		result = append(result, QueueRecoveryEntry{
			ID: entry.ID, SessionID: entry.SessionID, TaskID: entry.TaskID,
			Position: entry.Position, Content: entry.Content,
			ContentSHA256: hex.EncodeToString(digest[:]), ContentBytes: len(entry.Content),
			Model: entry.Model, PlanMode: entry.PlanMode, Attachments: entry.Attachments,
			QueuedAt: entry.QueuedAt.UTC().Format(time.RFC3339Nano), QueuedBy: entry.QueuedBy,
			Origin:           metadataString(entry.Metadata, "origin"),
			SenderTaskID:     metadataString(entry.Metadata, MetadataSenderTaskID),
			ReservedInFlight: entry.IsReservedInFlight(),
		})
	}
	return result
}

func recoveryMetadata(metadata map[string]interface{}, sourceSessionID string, sourcePosition int64) map[string]interface{} {
	recovered := clearReservedMetadata(metadata)
	if _, exists := recovered[MetadataRecoverySourceSessionID]; !exists {
		recovered[MetadataRecoverySourceSessionID] = sourceSessionID
		recovered[MetadataRecoverySourcePosition] = sourcePosition
	}
	return recovered
}

// Census returns a content-free FIFO snapshot for one session.
func (s *Service) Census(ctx context.Context, sessionID string) (*QueueCensus, error) {
	entries, err := s.repo.ListBySession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list queue census: %w", err)
	}
	autoRun, err := s.repo.GetAutoRun(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("read queue auto-run: %w", err)
	}
	result := &QueueCensus{
		Entries: make([]QueueCensusEntry, 0, len(entries)),
		Max:     s.MaxPerSession(),
		AutoRun: autoRun,
	}
	for i := range entries {
		entry := &entries[i]
		if entry.IsReservedInFlight() {
			continue
		}
		contentDigest := sha256.Sum256([]byte(entry.Content))
		result.Entries = append(result.Entries, QueueCensusEntry{
			ID:              entry.ID,
			Claim:           queueSnapshotClaim(entry),
			Position:        entry.Position,
			QueuedAt:        entry.QueuedAt.UTC().Format(time.RFC3339Nano),
			QueuedBy:        entry.QueuedBy,
			Origin:          metadataString(entry.Metadata, "origin"),
			SenderTaskID:    metadataString(entry.Metadata, MetadataSenderTaskID),
			RoutineWake:     metadataBool(entry.Metadata, MetadataRoutineWake),
			RoutineIdentity: metadataString(entry.Metadata, MetadataRoutineIdentity),
			ContentSHA256:   hex.EncodeToString(contentDigest[:]),
			ContentBytes:    len(entry.Content),
			AttachmentCount: len(entry.Attachments),
		})
	}
	result.BeforeCount = len(result.Entries)
	return result, nil
}

func metadataBool(metadata map[string]interface{}, key string) bool {
	value, _ := metadata[key].(bool)
	return value
}

// DisposeExact atomically removes only unchanged exact entries. Empty,
// duplicate, or malformed claims are rejected before repository mutation.
func (s *Service) DisposeExact(ctx context.Context, sessionID string, claims []QueueEntryClaim) (*QueueDispositionResult, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("%w: session id is required", ErrInvalidQueueDisposition)
	}
	if len(claims) == 0 {
		return nil, fmt.Errorf("%w: at least one queue entry claim is required", ErrInvalidQueueDisposition)
	}
	seen := make(map[string]struct{}, len(claims))
	for _, claim := range claims {
		if claim.ID == "" || claim.Claim == "" {
			return nil, fmt.Errorf("%w: queue entry id and claim are required", ErrInvalidQueueDisposition)
		}
		if _, exists := seen[claim.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate queue entry id: %s", ErrInvalidQueueDisposition, claim.ID)
		}
		seen[claim.ID] = struct{}{}
	}
	result, err := s.repo.DisposeExact(ctx, sessionID, claims)
	if err != nil {
		return nil, err
	}
	s.logger.Info("exact queue disposition completed",
		zap.String("session_id", sessionID),
		zap.Int("before_count", result.BeforeCount),
		zap.Int("after_count", result.AfterCount),
		zap.Any("outcomes", result.Outcomes))
	return result, nil
}

func queueSnapshotClaim(entry *QueuedMessage) string {
	if entry == nil {
		return ""
	}
	encoded, err := json.Marshal(entry)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func visibleQueueCount(entries []*QueuedMessage) int {
	count := 0
	for _, entry := range entries {
		if entry != nil && !entry.IsReservedInFlight() {
			count++
		}
	}
	return count
}
