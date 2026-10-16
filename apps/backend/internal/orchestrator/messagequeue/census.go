package messagequeue

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"
)

const (
	censusSnapshotVersion = 1
	censusClaimDomain     = "queue-entry-claim/v1\x00"
	censusClaimFieldLimit = 256
	censusMaxVisibleRows  = 128
	censusMaxStoredRows   = 1024
	censusMaxResultBytes  = 65536
)

var ErrCensusResourceExhausted = errors.New("queue census exceeds representation limit")

// PendingCensusEntry is the body-free evidence for one visible pending entry.
// The claim is derived from identity and the session revision; callers never receive
// message content, attachments, metadata, or delivery-reservation data.
type PendingCensusEntry struct {
	ID          string    `json:"id"`
	Position    int64     `json:"position"`
	QueuedBy    string    `json:"queued_by"`
	QueuedAt    time.Time `json:"queued_at"`
	ContentHash string    `json:"content_hash"`
	ContentSize int       `json:"content_size"`
	ClaimSHA256 string    `json:"claim_sha256"`
}

// CensusEntryClaim derives opaque comparison evidence for one entry.
func CensusEntryClaim(identity QueueSessionIdentity, executionID, entryID string, revision int64) (string, error) {
	fields := []string{identity.TaskID, identity.SessionID, identity.SessionIncarnationID, executionID, entryID}
	if revision < 0 || !validCensusClaimFields(fields) {
		return "", fmt.Errorf("invalid census claim evidence")
	}
	encoded := make([]byte, 0, len(censusClaimDomain)+len(fields)*4+32)
	encoded = append(encoded, censusClaimDomain...)
	for _, field := range fields {
		encoded = appendUint32(encoded, uint32(len(field)))
		encoded = append(encoded, field...)
	}
	encoded = appendUint64(encoded, uint64(revision))
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func validCensusClaimFields(fields []string) bool {
	for _, field := range fields {
		if field == "" || len(field) > censusClaimFieldLimit || !utf8.ValidString(field) {
			return false
		}
	}
	return true
}

func appendUint32(target []byte, value uint32) []byte {
	return append(target, byte(value>>24), byte(value>>16), byte(value>>8), byte(value))
}

func appendUint64(target []byte, value uint64) []byte {
	return append(target, byte(value>>56), byte(value>>48), byte(value>>40), byte(value>>32), byte(value>>24), byte(value>>16), byte(value>>8), byte(value))
}

// PendingCensus is one coherent, body-free observation. Revision must advance
// for every queue mutation. SnapshotSHA256 is diagnostic only; each entry's
// ClaimSHA256 is the removal comparison evidence.
type PendingCensus struct {
	Version             int                  `json:"version"`
	Identity            QueueSessionIdentity `json:"identity"`
	OriginalExecutionID string               `json:"original_execution_id"`
	Revision            int64                `json:"revision"`
	Count               int                  `json:"count"`
	Capacity            int                  `json:"capacity"`
	SnapshotSHA256      string               `json:"-"`
	Entries             []PendingCensusEntry `json:"entries"`
}

// NewPendingCensusEntry projects one visible row without materializing body
// fields. Its claim binds the identity, original execution, row ID and
// all-writer revision; it must be recomputed by the guarded removal effect.
func NewPendingCensusEntry(identity QueueSessionIdentity, executionID string, revision int64, message QueuedMessage) (PendingCensusEntry, error) {
	if message.ID == "" || message.Position < 1 || message.QueuedBy == "" || message.QueuedAt.IsZero() ||
		!validCensusClaimFields([]string{message.ID, message.QueuedBy}) {
		return PendingCensusEntry{}, ErrCensusResourceExhausted
	}
	claim, err := CensusEntryClaim(identity, executionID, message.ID, revision)
	if err != nil {
		return PendingCensusEntry{}, err
	}
	contentDigest := sha256.Sum256([]byte(message.Content))
	return PendingCensusEntry{
		ID: message.ID, Position: message.Position, QueuedBy: message.QueuedBy,
		QueuedAt: message.QueuedAt.UTC(), ContentHash: hex.EncodeToString(contentDigest[:]),
		ContentSize: len(message.Content), ClaimSHA256: claim,
	}, nil
}

type pendingCensusMaterial struct {
	Version             int                  `json:"version"`
	Identity            QueueSessionIdentity `json:"identity"`
	OriginalExecutionID string               `json:"original_execution_id"`
	Revision            int64                `json:"revision"`
	Count               int                  `json:"count"`
	Capacity            int                  `json:"capacity"`
	Entries             []PendingCensusEntry `json:"entries"`
}

// Finalize validates a complete observation and calculates its stable opaque
// claim. The result is complete-or-error; callers must never receive a slice.
func (c *PendingCensus) Finalize() error {
	if err := validatePendingCensus(c); err != nil {
		return err
	}
	encoded, err := json.Marshal(pendingCensusMaterial{
		Version:             c.Version,
		Identity:            c.Identity,
		OriginalExecutionID: c.OriginalExecutionID,
		Revision:            c.Revision,
		Count:               c.Count,
		Capacity:            c.Capacity,
		Entries:             c.Entries,
	})
	if err != nil {
		return fmt.Errorf("encode pending census: %w", err)
	}
	digest := sha256.Sum256(encoded)
	c.SnapshotSHA256 = hex.EncodeToString(digest[:])
	projection, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("encode pending census projection: %w", err)
	}
	if len(projection) > censusMaxResultBytes {
		return ErrCensusResourceExhausted
	}
	return nil
}

func validatePendingCensus(c *PendingCensus) error {
	if !validPendingCensusHeader(c) {
		return fmt.Errorf("invalid census version")
	}
	if c.Count > censusMaxVisibleRows {
		return ErrCensusResourceExhausted
	}
	for index, entry := range c.Entries {
		if !validPendingCensusEntry(entry) {
			return fmt.Errorf("invalid census entry %d", index)
		}
		if index > 0 && (c.Entries[index-1].Position > entry.Position ||
			(c.Entries[index-1].Position == entry.Position && c.Entries[index-1].ID >= entry.ID)) {
			return fmt.Errorf("census entries are not FIFO ordered")
		}
	}
	return nil
}

func validPendingCensusHeader(c *PendingCensus) bool {
	return c != nil && c.Version == censusSnapshotVersion &&
		validCensusClaimFields([]string{
			c.Identity.TaskID, c.Identity.SessionID, c.Identity.SessionIncarnationID, c.OriginalExecutionID,
		}) && c.Revision >= 0 && c.Capacity >= 0 && c.Count == len(c.Entries)
}

func validPendingCensusEntry(entry PendingCensusEntry) bool {
	return validCensusClaimFields([]string{entry.ID, entry.QueuedBy}) && entry.Position >= 1 && !entry.QueuedAt.IsZero() &&
		validCensusDigest(entry.ClaimSHA256) && validCensusDigest(entry.ContentHash) && entry.ContentSize >= 0
}

func validCensusDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}
