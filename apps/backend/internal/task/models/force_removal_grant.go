package models

import "time"

// ForceRemovalGrant is a server-issued, single-target authorization for a
// future agent admission. It is not a task removal or a cleanup instruction.
type ForceRemovalGrant struct {
	ID              string
	TaskID          string
	WorkspaceID     string
	TaskGeneration  time.Time
	CallerTaskID    string
	CallerSessionID string
	IssuedByUserID  string
	ExpiresAt       time.Time
	ConsumedAt      *time.Time
	CreatedAt       time.Time
}
