package messagequeue

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

// QueueCensusEntry describes a pending entry without exposing its body.
type QueueCensusEntry struct {
	ID              string `json:"id"`
	Position        int64  `json:"position"`
	QueuedAt        string `json:"queued_at"`
	QueuedBy        string `json:"queued_by"`
	ContentSHA256   string `json:"content_sha256"`
	ContentBytes    int    `json:"content_bytes"`
	AttachmentCount int    `json:"attachment_count"`
}

type QueueCensus struct {
	Entries     []QueueCensusEntry `json:"entries"`
	BeforeCount int                `json:"before_count"`
	Max         int                `json:"max"`
	AutoRun     bool               `json:"auto_run"`
}

// Census reads the current session's visible pending FIFO entries.
func (s *Service) Census(ctx context.Context, sessionID string) (*QueueCensus, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("session id is required")
	}
	entries, err := s.repo.ListBySession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list queue census: %w", err)
	}
	autoRun, err := s.repo.GetAutoRun(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("read queue auto-run: %w", err)
	}
	result := &QueueCensus{Entries: make([]QueueCensusEntry, 0, len(entries)), Max: s.MaxPerSession(), AutoRun: autoRun}
	for i := range entries {
		entry := &entries[i]
		if entry.SessionID != sessionID {
			return nil, fmt.Errorf("queue census entry belongs to another session")
		}
		if entry.IsReservedInFlight() {
			continue
		}
		digest := sha256.Sum256([]byte(entry.Content))
		result.Entries = append(result.Entries, QueueCensusEntry{
			ID: entry.ID, Position: entry.Position, QueuedAt: entry.QueuedAt.UTC().Format(time.RFC3339Nano),
			QueuedBy: entry.QueuedBy, ContentSHA256: hex.EncodeToString(digest[:]),
			ContentBytes: len(entry.Content), AttachmentCount: len(entry.Attachments),
		})
	}
	result.BeforeCount = len(result.Entries)
	return result, nil
}
