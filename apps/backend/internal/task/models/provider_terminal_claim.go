package models

// TerminalProviderAccessClaim identifies the session owner that may fence
// provider access before committing a terminal state.
type TerminalProviderAccessClaim struct {
	TaskID                 string
	SessionID              string
	AgentExecutionID       string
	RequireExecution       bool
	RequireNoExecution     bool
	RequireNoStartAttempt  bool
	ExpectedState          TaskSessionState
	ExpectedErrorStamp     string
	CheckErrorStamp        bool
	ExpectedStartAttemptID string
	TargetState            TaskSessionState
	ErrorMessage           string
}

// TerminalProviderAccessRecovery is the non-secret durable reservation left
// for startup reconciliation when a terminal transition is interrupted.
type TerminalProviderAccessRecovery struct {
	TaskID        string           `json:"task_id"`
	SessionID     string           `json:"session_id"`
	ClaimID       string           `json:"claim_id"`
	ExpectedState TaskSessionState `json:"expected_state"`
	TargetState   TaskSessionState `json:"target_state"`
	ErrorMessage  string           `json:"error_message"`
}
