package models

// TerminalProviderAccessClaim identifies the session owner that may fence
// provider access before committing a terminal state.
type TerminalProviderAccessClaim struct {
	TaskID                 string
	SessionID              string
	AgentExecutionID       string
	RequireExecution       bool
	ExpectedState          TaskSessionState
	ExpectedErrorStamp     string
	CheckErrorStamp        bool
	ExpectedStartAttemptID string
}
