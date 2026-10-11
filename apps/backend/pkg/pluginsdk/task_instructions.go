package pluginsdk

import (
	"context"

	pluginv1 "github.com/kandev/kandev/proto/kandev/plugin/v1"
)

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

type grpcExactTaskInstructionsManager struct{ client pluginv1.HostClient }

func (m grpcExactTaskInstructionsManager) Get(ctx context.Context, input ExactTaskInstructionsGet) (TaskInstructionBinding, error) {
	response, err := m.client.GetTaskInstructionsExact(ctx, &pluginv1.GetTaskInstructionsExactRequest{
		WorkspaceId: input.WorkspaceID, TaskId: input.TaskID, ApprovalRevision: input.ApprovalRevision, ManifestDigest: input.ManifestDigest,
	})
	if err != nil {
		return TaskInstructionBinding{}, err
	}
	return taskInstructionBindingFromProto(response.GetBinding()), nil
}

func (m grpcExactTaskInstructionsManager) Put(ctx context.Context, input ExactTaskInstructionsPut) (*CommandResult, TaskInstructionBinding, error) {
	response, err := m.client.PutTaskInstructionsExact(ctx, &pluginv1.PutTaskInstructionsExactRequest{
		RequestId: input.RequestID, WorkspaceId: input.WorkspaceID, TaskId: input.TaskID, EnvironmentId: input.EnvironmentID,
		ExpectedTaskResourceVersion: input.ExpectedTaskResourceVersion, ExpectedOwnershipGeneration: input.ExpectedOwnershipGeneration,
		ExpectedVersion: input.ExpectedVersion, ManagementInstanceKey: input.ManagementInstanceKey,
		ExpectedClaimGeneration: input.ExpectedClaimGeneration, Content: input.Content, IdempotencyKey: input.IdempotencyKey,
		ApprovalRevision: input.ApprovalRevision, ManifestDigest: input.ManifestDigest,
	})
	if err != nil {
		return nil, TaskInstructionBinding{}, err
	}
	return commandResultFromProto(response.GetResult()), taskInstructionBindingFromProto(response.GetBinding()), nil
}

func taskInstructionBindingFromProto(value *pluginv1.TaskInstructionBinding) TaskInstructionBinding {
	if value == nil {
		return TaskInstructionBinding{}
	}
	return TaskInstructionBinding{TaskID: value.GetTaskId(), WorkspaceID: value.GetWorkspaceId(), EnvironmentID: value.GetEnvironmentId(),
		OwnershipGeneration: value.GetOwnershipGeneration(), Version: value.GetVersion(), OwnerInstallationID: value.GetOwnerInstallationId(),
		OwnerInstanceKey: value.GetOwnerInstanceKey(), Content: value.GetContent(), Digest: value.GetDigest(),
		ControlPath: value.GetControlPath(), GuardKind: value.GetGuardKind()}
}

func taskInstructionBindingToProto(value TaskInstructionBinding) *pluginv1.TaskInstructionBinding {
	return &pluginv1.TaskInstructionBinding{TaskId: value.TaskID, WorkspaceId: value.WorkspaceID, EnvironmentId: value.EnvironmentID,
		OwnershipGeneration: value.OwnershipGeneration, Version: value.Version, OwnerInstallationId: value.OwnerInstallationID,
		OwnerInstanceKey: value.OwnerInstanceKey, Content: value.Content, Digest: value.Digest,
		ControlPath: value.ControlPath, GuardKind: value.GuardKind}
}
