package pluginsdk

import "context"

// ExactTaskInstructionsPut replaces one Coordinator-owned task instruction
// binding at its observed task, claim, environment, and binding revisions.
type ExactTaskInstructionsPut struct {
	RequestID, WorkspaceID, TaskID, EnvironmentID string
	ExpectedTaskResourceVersion                   string
	ExpectedOwnershipGeneration, ExpectedVersion  int64
	ManagementInstanceKey                         string
	ExpectedClaimGeneration                       int64
	Content, IdempotencyKey                       string
	ApprovalRevision                              uint64
	ManifestDigest                                string
}

// ExactTaskInstructionsGet identifies one task-local instruction binding.
type ExactTaskInstructionsGet struct {
	WorkspaceID, TaskID string
	ApprovalRevision    uint64
	ManifestDigest      string
}

// TaskInstructionBinding is the Host-authoritative projection returned to a
// Coordinator. Filesystem publication is separately verified before a prompt
// is admitted.
type TaskInstructionBinding struct {
	TaskID, WorkspaceID, EnvironmentID      string
	OwnershipGeneration, Version            int64
	OwnerInstallationID, OwnerInstanceKey   string
	Content, Digest, ControlPath, GuardKind string
}

// ExactTaskInstructionsManager exposes the narrowly scoped Coordinator
// instruction binding operations.
type ExactTaskInstructionsManager interface {
	Get(context.Context, ExactTaskInstructionsGet) (TaskInstructionBinding, error)
	Put(context.Context, ExactTaskInstructionsPut) (*CommandResult, TaskInstructionBinding, error)
}

// ExactTaskInstructionsHost is an additive Host extension for task-local
// Coordinator instructions.
type ExactTaskInstructionsHost interface {
	TaskInstructions() ExactTaskInstructionsManager
}

// HostTaskInstructions returns the instruction extension when available.
func HostTaskInstructions(host Host) (ExactTaskInstructionsManager, bool) {
	extended, ok := host.(ExactTaskInstructionsHost)
	if !ok || extended.TaskInstructions() == nil {
		return nil, false
	}
	return extended.TaskInstructions(), true
}
