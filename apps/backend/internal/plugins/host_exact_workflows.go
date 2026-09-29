package plugins

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	wfmodels "github.com/kandev/kandev/internal/workflow/models"
	"github.com/kandev/kandev/pkg/pluginsdk"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const exactWorkflowPageLimit int32 = 100

func (h *pluginHost) ListWorkflowsExact(ctx context.Context, query pluginsdk.ExactWorkflowQuery) ([]pluginsdk.Workflow, *pluginsdk.ExactPageInfo, error) {
	receipt, err := h.authorizeExactReadDecision(query.WorkspaceID, query.CapabilityRevision, "host.v2.read:workflows", CanonicalApprovalDigest("workflows", query.WorkspaceID, query.Page.SnapshotVersion))
	if err != nil {
		return nil, nil, err
	}
	if h.workflows == nil || h.exactSnapshots == nil {
		return nil, nil, resourceExhausted("exact workflow read is unavailable")
	}
	rows, err := h.workflows.ListWorkflows(ctx, query.WorkspaceID, false)
	if err != nil {
		return nil, nil, err
	}
	items := make([]pluginsdk.Workflow, len(rows))
	for i, row := range rows {
		if row == nil || row.WorkspaceID != query.WorkspaceID {
			return nil, nil, status.Error(codes.FailedPrecondition, "exact workflow projection is incomplete")
		}
		items[i] = workflowModelToDTO(row)
	}
	version, err := exactProjectionDigest(items)
	if err != nil {
		return nil, nil, status.Error(codes.FailedPrecondition, "exact workflow projection is incomplete")
	}
	items, info, err := h.pageExactWorkflows(query.WorkspaceID, query.CapabilityRevision, "workflows", version, query.Page, items)
	if err != nil {
		return nil, nil, err
	}
	if err := h.recordExactPageRead(info, receipt); err != nil {
		return nil, nil, err
	}
	return items, info, nil
}

func (h *pluginHost) ListWorkflowStepsExact(ctx context.Context, query pluginsdk.ExactWorkflowStepsQuery) ([]pluginsdk.WorkflowStep, *pluginsdk.ExactPageInfo, error) {
	receipt, err := h.authorizeExactReadDecision(query.WorkspaceID, query.CapabilityRevision, "host.v2.read:workflows", CanonicalApprovalDigest("workflow-steps", query.WorkspaceID, query.WorkflowID, query.Page.SnapshotVersion))
	if err != nil {
		return nil, nil, err
	}
	if h.workflows == nil || h.workflowSteps == nil || h.exactSnapshots == nil {
		return nil, nil, resourceExhausted("exact workflow step read is unavailable")
	}
	workflows, err := h.workflows.ListWorkflows(ctx, query.WorkspaceID, false)
	if err != nil {
		return nil, nil, err
	}
	found := false
	for _, workflow := range workflows {
		if workflow != nil && workflow.ID == query.WorkflowID && workflow.WorkspaceID == query.WorkspaceID {
			found = true
			break
		}
	}
	if !found {
		return nil, nil, status.Error(codes.NotFound, "workflow not found")
	}
	rows, err := h.workflowSteps.ListStepsByWorkflow(ctx, query.WorkflowID)
	if err != nil {
		return nil, nil, err
	}
	items, version, err := exactWorkflowStepProjection(rows, query.WorkflowID)
	if err != nil {
		return nil, nil, err
	}
	items, info, err := h.pageExactWorkflowSteps(query.WorkspaceID, query.CapabilityRevision, "workflow-steps:"+query.WorkflowID, version, query.Page, items)
	if err != nil {
		return nil, nil, err
	}
	if err := h.recordExactPageRead(info, receipt); err != nil {
		return nil, nil, err
	}
	return items, info, nil
}

func exactWorkflowStepProjection(rows []*wfmodels.WorkflowStep, workflowID string) ([]pluginsdk.WorkflowStep, string, error) {
	items := make([]pluginsdk.WorkflowStep, len(rows))
	for i, row := range rows {
		if row == nil || row.WorkflowID != workflowID {
			return nil, "", status.Error(codes.FailedPrecondition, "exact workflow step projection is incomplete")
		}
		items[i] = workflowStepModelToDTO(row)
	}
	version, err := exactProjectionDigest(items)
	if err != nil {
		return nil, "", status.Error(codes.FailedPrecondition, "exact workflow step projection is incomplete")
	}
	return items, version, nil
}

func exactProjectionDigest(items any) (string, error) {
	payload, err := json.Marshal(items)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func (h *pluginHost) exactPageBinding(workspaceID string, revision uint64, filter, version string) exactSnapshotBinding {
	return exactSnapshotBinding{InstallationID: h.installationID, WorkspaceID: workspaceID, FilterDigest: filter, ApprovalRevision: revision, ProjectionVersion: version}
}

func (h *pluginHost) exactOffset(binding exactSnapshotBinding, page pluginsdk.ExactPage) (int, error) {
	if page.Limit < 0 || page.Limit > exactWorkflowPageLimit || (page.SnapshotVersion != "" && page.SnapshotVersion != binding.ProjectionVersion) {
		return 0, status.Error(codes.InvalidArgument, "exact snapshot page is invalid")
	}
	if page.Cursor == "" {
		return 0, nil
	}
	offset, err := h.exactSnapshots.offset(page.Cursor, binding)
	if err != nil {
		return 0, status.Error(codes.InvalidArgument, "exact snapshot cursor is invalid")
	}
	return offset, nil
}

func (h *pluginHost) exactInfo(binding exactSnapshotBinding, page pluginsdk.ExactPage, total, offset int) (*pluginsdk.ExactPageInfo, int, error) {
	limit := int(page.Limit)
	if limit == 0 {
		limit = int(exactWorkflowPageLimit)
	}
	if offset > total {
		return nil, 0, status.Error(codes.InvalidArgument, "exact snapshot cursor is invalid")
	}
	end := offset + limit
	if end > total {
		end = total
	}
	info := &pluginsdk.ExactPageInfo{SnapshotVersion: binding.ProjectionVersion, HasMore: end < total}
	if info.HasMore {
		cursor, err := h.exactSnapshots.create(binding, end)
		if err != nil {
			return nil, 0, err
		}
		info.NextCursor = cursor
	}
	return info, end, nil
}

func (h *pluginHost) pageExactWorkflows(workspaceID string, revision uint64, filter, version string, page pluginsdk.ExactPage, items []pluginsdk.Workflow) ([]pluginsdk.Workflow, *pluginsdk.ExactPageInfo, error) {
	binding := h.exactPageBinding(workspaceID, revision, filter, version)
	offset, err := h.exactOffset(binding, page)
	if err != nil {
		return nil, nil, err
	}
	info, end, err := h.exactInfo(binding, page, len(items), offset)
	if err != nil {
		return nil, nil, err
	}
	return items[offset:end], info, nil
}
func (h *pluginHost) pageExactWorkflowSteps(workspaceID string, revision uint64, filter, version string, page pluginsdk.ExactPage, items []pluginsdk.WorkflowStep) ([]pluginsdk.WorkflowStep, *pluginsdk.ExactPageInfo, error) {
	binding := h.exactPageBinding(workspaceID, revision, filter, version)
	offset, err := h.exactOffset(binding, page)
	if err != nil {
		return nil, nil, err
	}
	info, end, err := h.exactInfo(binding, page, len(items), offset)
	if err != nil {
		return nil, nil, err
	}
	return items[offset:end], info, nil
}
