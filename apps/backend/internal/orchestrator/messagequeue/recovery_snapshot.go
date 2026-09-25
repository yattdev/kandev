package messagequeue

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"
)

const (
	DefaultRecoveryPageBodyBytes = 64 * 1024
	MinRecoveryPageBodyBytes     = 1024
	MaxRecoveryPageBodyBytes     = 256 * 1024
)

var (
	ErrInvalidQueueRecoveryPage = errors.New("invalid queue recovery page request")
	ErrQueueRecoveryChanged     = errors.New("queue changed during recovery")
)

// QueueRecoveryFragment carries one bounded UTF-8 slice of an immutable queue
// entry body. Attachments and metadata are intentionally represented only by a
// count: terminal-session recovery is a message-body capability, not an
// attachment or unrestricted queue-record API.
type QueueRecoveryFragment struct {
	ID               string `json:"id"`
	Claim            string `json:"claim"`
	Position         int64  `json:"position"`
	QueuedAt         string `json:"queued_at"`
	QueuedBy         string `json:"queued_by"`
	ContentSHA256    string `json:"content_sha256"`
	ContentBytes     int    `json:"content_bytes"`
	AttachmentCount  int    `json:"attachment_count"`
	ReservedInFlight bool   `json:"reserved_in_flight"`
	Body             string `json:"body"`
	BodyOffsetBytes  int    `json:"body_offset_bytes"`
	BodyBytes        int    `json:"body_bytes"`
	BodyComplete     bool   `json:"body_complete"`
}

// QueueRecoveryPage is a stable page from one exact ordered queue snapshot.
// NextCursor embeds the snapshot digest and fails closed if any source entry
// changes between calls.
type QueueRecoveryPage struct {
	SnapshotSHA256    string                  `json:"snapshot_sha256"`
	TotalEntries      int                     `json:"total_entries"`
	TotalContentBytes int                     `json:"total_content_bytes"`
	PageBodyBytes     int                     `json:"page_body_bytes"`
	Fragments         []QueueRecoveryFragment `json:"fragments"`
	NextCursor        string                  `json:"next_cursor,omitempty"`
}

// QueueRecoveryManifest is a body-free inventory of every stored entry,
// including entries currently reserved by a consumer.
type QueueRecoveryEntry struct {
	ID               string `json:"id"`
	Claim            string `json:"claim"`
	Position         int64  `json:"position"`
	QueuedAt         string `json:"queued_at"`
	QueuedBy         string `json:"queued_by"`
	ContentSHA256    string `json:"content_sha256"`
	ContentBytes     int    `json:"content_bytes"`
	AttachmentCount  int    `json:"attachment_count"`
	ReservedInFlight bool   `json:"reserved_in_flight"`
}

type QueueRecoveryManifest struct {
	SnapshotSHA256    string               `json:"snapshot_sha256"`
	TotalEntries      int                  `json:"total_entries"`
	TotalContentBytes int                  `json:"total_content_bytes"`
	Entries           []QueueRecoveryEntry `json:"entries"`
}

func (s *Service) RecoverySnapshotManifest(ctx context.Context, sessionID string) (*QueueRecoveryManifest, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("%w: session id is required", ErrInvalidQueueRecoveryPage)
	}
	entries, err := s.repo.ListBySession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list queue recovery manifest: %w", err)
	}
	snapshot, totalBytes, err := queueRecoveryManifest(sessionID, entries)
	if err != nil {
		return nil, err
	}
	result := &QueueRecoveryManifest{SnapshotSHA256: snapshot, TotalEntries: len(entries), TotalContentBytes: totalBytes, Entries: make([]QueueRecoveryEntry, 0, len(entries))}
	for i := range entries {
		entry := &entries[i]
		digest := sha256.Sum256([]byte(entry.Content))
		result.Entries = append(result.Entries, QueueRecoveryEntry{
			ID: entry.ID, Claim: queueSnapshotClaim(entry), Position: entry.Position,
			QueuedAt: entry.QueuedAt.UTC().Format(recoveryTimestampFormat), QueuedBy: entry.QueuedBy,
			ContentSHA256: hex.EncodeToString(digest[:]), ContentBytes: len(entry.Content),
			AttachmentCount: len(entry.Attachments), ReservedInFlight: entry.IsReservedInFlight(),
		})
	}
	return result, nil
}

type queueRecoveryCursor struct {
	Version        int    `json:"v"`
	SessionID      string `json:"session_id"`
	SnapshotSHA256 string `json:"snapshot_sha256"`
	EntryIndex     int    `json:"entry_index"`
	BodyOffset     int    `json:"body_offset"`
}

// RecoverySnapshotPage returns message bodies without mutating, reserving, or
// acknowledging queue entries. Authorization and terminal-state validation
// belong to the MCP handler, which resolves the caller principal and session
// row before invoking this repository-facing operation.
//
//nolint:gocognit,cyclop,funlen // UTF-8 paging and immutable cursor checks share this bounded loop.
func (s *Service) RecoverySnapshotPage(
	ctx context.Context,
	sessionID string,
	cursor string,
	maxBodyBytes int,
) (*QueueRecoveryPage, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("%w: session id is required", ErrInvalidQueueRecoveryPage)
	}
	if maxBodyBytes == 0 {
		maxBodyBytes = DefaultRecoveryPageBodyBytes
	}
	if maxBodyBytes < MinRecoveryPageBodyBytes || maxBodyBytes > MaxRecoveryPageBodyBytes {
		return nil, fmt.Errorf("%w: max_body_bytes must be between %d and %d",
			ErrInvalidQueueRecoveryPage, MinRecoveryPageBodyBytes, MaxRecoveryPageBodyBytes)
	}

	entries, err := s.repo.ListBySession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list queue recovery snapshot: %w", err)
	}
	snapshotSHA, totalBytes, err := queueRecoveryManifest(sessionID, entries)
	if err != nil {
		return nil, err
	}

	position, err := decodeQueueRecoveryCursor(cursor, sessionID, snapshotSHA, entries)
	if err != nil {
		return nil, err
	}
	page := &QueueRecoveryPage{
		SnapshotSHA256:    snapshotSHA,
		TotalEntries:      len(entries),
		TotalContentBytes: totalBytes,
		Fragments:         make([]QueueRecoveryFragment, 0),
	}

	for position.EntryIndex < len(entries) && page.PageBodyBytes < maxBodyBytes {
		entry := &entries[position.EntryIndex]
		if !utf8.ValidString(entry.Content) {
			return nil, fmt.Errorf("queue recovery entry %s is not valid UTF-8", entry.ID)
		}
		if position.BodyOffset < 0 || position.BodyOffset > len(entry.Content) ||
			!utf8.ValidString(entry.Content[:position.BodyOffset]) {
			return nil, fmt.Errorf("%w: cursor body offset is invalid", ErrInvalidQueueRecoveryPage)
		}

		remainingBudget := maxBodyBytes - page.PageBodyBytes
		end := len(entry.Content)
		if end-position.BodyOffset > remainingBudget {
			end = position.BodyOffset + remainingBudget
			for end > position.BodyOffset && !utf8.ValidString(entry.Content[position.BodyOffset:end]) {
				end--
			}
			if end == position.BodyOffset {
				if page.PageBodyBytes > 0 {
					break
				}
				return nil, fmt.Errorf("%w: body budget cannot contain the next UTF-8 code point", ErrInvalidQueueRecoveryPage)
			}
		}

		body := entry.Content[position.BodyOffset:end]
		contentDigest := sha256.Sum256([]byte(entry.Content))
		fragment := QueueRecoveryFragment{
			ID:               entry.ID,
			Claim:            queueSnapshotClaim(entry),
			Position:         entry.Position,
			QueuedAt:         entry.QueuedAt.UTC().Format(recoveryTimestampFormat),
			QueuedBy:         entry.QueuedBy,
			ContentSHA256:    hex.EncodeToString(contentDigest[:]),
			ContentBytes:     len(entry.Content),
			AttachmentCount:  len(entry.Attachments),
			ReservedInFlight: entry.IsReservedInFlight(),
			Body:             body,
			BodyOffsetBytes:  position.BodyOffset,
			BodyBytes:        len(body),
			BodyComplete:     end == len(entry.Content),
		}
		page.Fragments = append(page.Fragments, fragment)
		page.PageBodyBytes += len(body)

		if fragment.BodyComplete {
			position.EntryIndex++
			position.BodyOffset = 0
		} else {
			position.BodyOffset = end
		}
		// Empty bodies still produce exactly one complete fragment. A full page
		// stops before starting another entry, keeping the requested byte bound.
		if page.PageBodyBytes >= maxBodyBytes {
			break
		}
	}

	if position.EntryIndex < len(entries) {
		page.NextCursor, err = encodeQueueRecoveryCursor(position)
		if err != nil {
			return nil, err
		}
	}
	return page, nil
}

const recoveryTimestampFormat = "2006-01-02T15:04:05.999999999Z07:00"

func queueRecoveryManifest(sessionID string, entries []QueuedMessage) (string, int, error) {
	hash := sha256.New()
	totalBytes := 0
	for i := range entries {
		entry := &entries[i]
		if entry.SessionID != sessionID {
			return "", 0, fmt.Errorf("queue recovery entry %s belongs to another session", entry.ID)
		}
		claim := queueSnapshotClaim(entry)
		if claim == "" {
			return "", 0, fmt.Errorf("cannot hash queue recovery entry %s", entry.ID)
		}
		_, _ = fmt.Fprintf(hash, "%d:%s\n", len(claim), claim)
		totalBytes += len(entry.Content)
	}
	return hex.EncodeToString(hash.Sum(nil)), totalBytes, nil
}

func decodeQueueRecoveryCursor(
	raw string,
	sessionID string,
	snapshotSHA string,
	entries []QueuedMessage,
) (queueRecoveryCursor, error) {
	if raw == "" {
		return queueRecoveryCursor{
			Version: 1, SessionID: sessionID, SnapshotSHA256: snapshotSHA,
		}, nil
	}
	encoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return queueRecoveryCursor{}, fmt.Errorf("%w: cursor is not valid base64url", ErrInvalidQueueRecoveryPage)
	}
	var cursor queueRecoveryCursor
	if err := json.Unmarshal(encoded, &cursor); err != nil {
		return queueRecoveryCursor{}, fmt.Errorf("%w: cursor is not valid JSON", ErrInvalidQueueRecoveryPage)
	}
	if cursor.Version != 1 || cursor.SessionID != sessionID ||
		cursor.EntryIndex < 0 || cursor.EntryIndex > len(entries) || cursor.BodyOffset < 0 {
		return queueRecoveryCursor{}, fmt.Errorf("%w: cursor identity is invalid", ErrInvalidQueueRecoveryPage)
	}
	if cursor.SnapshotSHA256 != snapshotSHA {
		return queueRecoveryCursor{}, ErrQueueRecoveryChanged
	}
	if cursor.EntryIndex == len(entries) && cursor.BodyOffset != 0 {
		return queueRecoveryCursor{}, fmt.Errorf("%w: completed cursor has a body offset", ErrInvalidQueueRecoveryPage)
	}
	return cursor, nil
}

func encodeQueueRecoveryCursor(cursor queueRecoveryCursor) (string, error) {
	encoded, err := json.Marshal(cursor)
	if err != nil {
		return "", fmt.Errorf("encode queue recovery cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
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
