package models

import "time"

const (
	CompletionIntentStatePending  = "pending"
	CompletionIntentStateSettling = "settling"
	CompletionIntentStateSettled  = "settled"
)

// CompletionIntent is the immutable identity captured with an accepted
// completion signal. Settlement may change its state, but never its source
// turn, workflow entry, or bounded signal payload.
type CompletionIntent struct {
	ID                       string
	WorkspaceID              string
	TaskID                   string
	SessionID                string
	TurnID                   string
	WorkflowStepID           string
	WorkflowStepTransitionID int64
	ExecutionID              string
	QueueIncarnationID       string
	Signal                   PendingStepCompletionSignal
	EligibleAt               time.Time
	State                    string
	Outcome                  string
	Version                  int64
	LeaseExpiresAt           *time.Time
	SettledAt                *time.Time
	CreatedAt                time.Time
	UpdatedAt                time.Time
}

// SessionControlEvent is the immutable audit record for an authorized parent
// recovery decision. It intentionally stores only evidence identifiers.
type SessionControlEvent struct {
	ID                       string
	WorkspaceID              string
	ActorTaskID              string
	ActorSessionID           string
	TargetTaskID             string
	TargetSessionID          string
	TargetTurnID             string
	CompletionIntentID       string
	WorkflowStepTransitionID *int64
	Authority                string
	EvidenceRevision         string
	EvidenceDigest           string
	Outcome                  string
	Reason                   string
	CreatedAt                time.Time
}
