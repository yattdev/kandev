package plugins

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/kandev/kandev/internal/plugins/state"
	"github.com/kandev/kandev/internal/task/taskinstructions"
	"github.com/kandev/kandev/pkg/pluginsdk"
)

const taskInstructionsReadCapability = "host.v2.read:task_instructions"
const taskInstructionsWriteCapability = "host.v2.write:task_instructions"

type taskInstructionBindingWriter interface {
	GetTaskInstructionBinding(context.Context, string) (*taskinstructions.DurableBinding, error)
	PutTaskInstructionBindingExact(context.Context, taskinstructions.WriteRequest) (*taskinstructions.DurableBinding, error)
}

type exactTaskInstructionsManager struct{ host *pluginHost }

func (h *pluginHost) TaskInstructions() pluginsdk.ExactTaskInstructionsManager {
	return exactTaskInstructionsManager{host: h}
}

func (m exactTaskInstructionsManager) Get(ctx context.Context, input pluginsdk.ExactTaskInstructionsGet) (pluginsdk.TaskInstructionBinding, error) {
	return m.host.getTaskInstructionsExact(ctx, input)
}

func (m exactTaskInstructionsManager) Put(ctx context.Context, input pluginsdk.ExactTaskInstructionsPut) (*pluginsdk.CommandResult, pluginsdk.TaskInstructionBinding, error) {
	return m.host.putTaskInstructionsExact(ctx, input)
}

func (h *pluginHost) getTaskInstructionsExact(ctx context.Context, input pluginsdk.ExactTaskInstructionsGet) (pluginsdk.TaskInstructionBinding, error) {
	if !validTaskInstructionsGet(input) || h.taskInstructions == nil || h.service == nil || h.installationID == "" {
		return pluginsdk.TaskInstructionBinding{}, taskinstructions.ErrBindingNotFound
	}
	if !h.taskInstructionsAllowed(input.WorkspaceID, taskInstructionsReadCapability, input.ApprovalRevision, input.ManifestDigest, "get") {
		return pluginsdk.TaskInstructionBinding{}, taskinstructions.ErrBindingNotFound
	}
	binding, err := h.taskInstructions.GetTaskInstructionBinding(ctx, input.TaskID)
	if err != nil || binding.WorkspaceID != input.WorkspaceID || binding.OwnerInstallationID != h.installationID {
		return pluginsdk.TaskInstructionBinding{}, taskinstructions.ErrBindingNotFound
	}
	return taskInstructionBindingToSDK(binding), nil
}

//nolint:cyclop // This command keeps approval, durable receipt, and CAS recovery together.
func (h *pluginHost) putTaskInstructionsExact(ctx context.Context, input pluginsdk.ExactTaskInstructionsPut) (*pluginsdk.CommandResult, pluginsdk.TaskInstructionBinding, error) {
	if !validTaskInstructionsPut(input) || h.taskInstructions == nil || h.service == nil || h.installationID == "" {
		return &pluginsdk.CommandResult{Status: pluginsdk.CommandInvalid, Reason: "invalid_request"}, pluginsdk.TaskInstructionBinding{}, nil
	}
	digest, err := taskInstructionsPayloadDigest(input)
	if err != nil {
		return &pluginsdk.CommandResult{Status: pluginsdk.CommandInvalid, Reason: "invalid_request"}, pluginsdk.TaskInstructionBinding{}, nil
	}
	if !h.taskInstructionsAllowed(input.WorkspaceID, taskInstructionsWriteCapability, input.ApprovalRevision, input.ManifestDigest, digest) {
		return &pluginsdk.CommandResult{Status: pluginsdk.CommandDenied, Reason: "capability_denied"}, pluginsdk.TaskInstructionBinding{}, nil
	}
	store := h.commandStore
	if store == nil {
		store = h.service.exactCommandStoreDep()
	}
	if store == nil {
		return &pluginsdk.CommandResult{Status: pluginsdk.CommandUnavailable, Reason: exactReasonCommandStoreUnavailable}, pluginsdk.TaskInstructionBinding{}, nil
	}
	record, replayed, err := store.Admit(ctx, state.CommandIntent{
		InstallationID: h.installationID, WorkspaceID: input.WorkspaceID, RequestID: input.RequestID,
		IdempotencyKey: input.IdempotencyKey, Method: exactPutTaskInstructionsMethod, CapabilityID: taskInstructionsWriteCapability,
		PayloadDigest: digest, TargetID: input.TaskID, ExpectedResourceVersion: instructionExpectedVersion(input),
		ApprovalRevision: input.ApprovalRevision, ManifestDigest: input.ManifestDigest,
	})
	if errors.Is(err, state.ErrCommandPayloadConflict) {
		return &pluginsdk.CommandResult{Status: pluginsdk.CommandConflict, Reason: "idempotency_payload_mismatch"}, pluginsdk.TaskInstructionBinding{}, nil
	}
	if err != nil {
		return &pluginsdk.CommandResult{Status: pluginsdk.CommandUnavailable, Reason: "command_admission_unavailable"}, pluginsdk.TaskInstructionBinding{}, nil
	}
	if replayed && record.Receipt.State == exactReceiptStateCompleted {
		binding, readErr := h.getTaskInstructionsExact(ctx, pluginsdk.ExactTaskInstructionsGet{WorkspaceID: input.WorkspaceID, TaskID: input.TaskID, ApprovalRevision: input.ApprovalRevision, ManifestDigest: input.ManifestDigest})
		if readErr != nil {
			return &pluginsdk.CommandResult{Status: pluginsdk.CommandUnavailable, Reason: "instruction_receipt_unavailable"}, pluginsdk.TaskInstructionBinding{}, nil
		}
		return commandResultFromRecord(record), binding, nil
	}
	request := taskinstructions.WriteRequest{
		TaskID: input.TaskID, WorkspaceID: input.WorkspaceID, EnvironmentID: input.EnvironmentID,
		ExpectedTaskResourceVersion: input.ExpectedTaskResourceVersion, ManagementInstallationID: h.installationID,
		ManagementInstanceKey: input.ManagementInstanceKey, ExpectedClaimGeneration: input.ExpectedClaimGeneration,
		ExpectedOwnershipGeneration: input.ExpectedOwnershipGeneration, ExpectedVersion: input.ExpectedVersion,
		Content: input.Content, Digest: taskinstructions.Digest(input.Content),
	}
	binding, err := h.taskInstructions.PutTaskInstructionBindingExact(ctx, request)
	if err != nil {
		return h.completeTaskInstructionsError(ctx, store, record, input.TaskID, err)
	}
	completed, err := store.Complete(ctx, record.Intent.OperationID, string(pluginsdk.CommandApplied), "", input.TaskID, strconv.FormatInt(binding.Version, 10))
	if err != nil {
		return &pluginsdk.CommandResult{Status: pluginsdk.CommandUnavailable, Reason: "command_receipt_unavailable"}, taskInstructionBindingToSDK(binding), nil
	}
	return commandResultFromRecord(completed), taskInstructionBindingToSDK(binding), nil
}

func validTaskInstructionsGet(input pluginsdk.ExactTaskInstructionsGet) bool {
	return isBoundedApprovalIdentifier(input.WorkspaceID) && isBoundedApprovalIdentifier(input.TaskID) && input.ApprovalRevision > 0 && validManifestDigest(input.ManifestDigest)
}

func validTaskInstructionsPut(input pluginsdk.ExactTaskInstructionsPut) bool {
	if !isBoundedApprovalIdentifier(input.RequestID) || !isBoundedApprovalIdentifier(input.WorkspaceID) ||
		!isBoundedApprovalIdentifier(input.TaskID) || !isBoundedApprovalIdentifier(input.EnvironmentID) ||
		!isBoundedApprovalIdentifier(input.ManagementInstanceKey) || !isBoundedApprovalIdentifier(input.IdempotencyKey) ||
		input.ApprovalRevision == 0 || !validManifestDigest(input.ManifestDigest) || input.ExpectedOwnershipGeneration < 1 ||
		input.ExpectedClaimGeneration < 1 || input.ExpectedVersion < 0 {
		return false
	}
	if _, err := time.Parse(time.RFC3339Nano, input.ExpectedTaskResourceVersion); err != nil {
		return false
	}
	return taskinstructions.Digest(input.Content) != "" && len(input.Content) <= 32*1024
}

func (h *pluginHost) taskInstructionsAllowed(workspaceID, capability string, revision uint64, manifestDigest, payload string) bool {
	installed := h.service.installedRecordByInstallationID(h.installationID)
	if installed == nil || ManifestCapabilityDigest(installed.Manifest) != manifestDigest {
		return false
	}
	return h.service.authorizePluginCapability(h.installationID, workspaceID, capability, revision, payload,
		CanonicalApprovalDigest("host-method", capability)).Allowed
}

func taskInstructionsPayloadDigest(input pluginsdk.ExactTaskInstructionsPut) (string, error) {
	payload := struct {
		WorkspaceID, TaskID, EnvironmentID, TaskVersion, InstanceKey, Content string
		OwnershipGeneration, Version, ClaimGeneration                         int64
	}{input.WorkspaceID, input.TaskID, input.EnvironmentID, input.ExpectedTaskResourceVersion, input.ManagementInstanceKey, input.Content,
		input.ExpectedOwnershipGeneration, input.ExpectedVersion, input.ExpectedClaimGeneration}
	encoded, err := json.Marshal(payload)
	if err != nil || len(encoded) > 65536 {
		return "", errors.New("invalid task instruction payload")
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func instructionExpectedVersion(input pluginsdk.ExactTaskInstructionsPut) string {
	return input.ExpectedTaskResourceVersion + "/" + strconv.FormatInt(input.ExpectedOwnershipGeneration, 10) + "/" + strconv.FormatInt(input.ExpectedVersion, 10)
}

func (h *pluginHost) completeTaskInstructionsError(ctx context.Context, store *state.CommandStore, record state.CommandRecord, taskID string, err error) (*pluginsdk.CommandResult, pluginsdk.TaskInstructionBinding, error) {
	status := pluginsdk.CommandUnavailable
	if errors.Is(err, taskinstructions.ErrBindingNotFound) {
		status = pluginsdk.CommandNotFound
	} else if errors.Is(err, taskinstructions.ErrVersionConflict) || errors.Is(err, taskinstructions.ErrTaskChanged) || errors.Is(err, taskinstructions.ErrManagementClaimChanged) || errors.Is(err, taskinstructions.ErrEnvironmentChanged) {
		status = pluginsdk.CommandConflict
	}
	completed, completeErr := store.Complete(ctx, record.Intent.OperationID, string(status), string(status), taskID, "")
	if completeErr != nil {
		return &pluginsdk.CommandResult{Status: pluginsdk.CommandUnavailable, Reason: "command_receipt_unavailable"}, pluginsdk.TaskInstructionBinding{}, nil
	}
	return commandResultFromRecord(completed), pluginsdk.TaskInstructionBinding{}, nil
}

func taskInstructionBindingToSDK(binding *taskinstructions.DurableBinding) pluginsdk.TaskInstructionBinding {
	return pluginsdk.TaskInstructionBinding{TaskID: binding.TaskID, WorkspaceID: binding.WorkspaceID, EnvironmentID: binding.EnvironmentID,
		OwnershipGeneration: binding.OwnershipGeneration, Version: binding.Version, OwnerInstallationID: binding.OwnerInstallationID,
		OwnerInstanceKey: binding.OwnerInstanceKey, Content: binding.Content, Digest: binding.Digest, ControlPath: binding.ControlPath, GuardKind: string(binding.GuardKind)}
}
