package models

import "time"

// TaskMessageRecord is a bounded sender-owned projection, never a conversation.
type TaskMessageRecord struct {
	Kind          string
	ID            string
	TaskID        string
	SessionID     string
	TurnID        string
	Metadata      map[string]any
	CreatedAt     time.Time
	ContentSHA256 string
}
