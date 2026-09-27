package plugins

import (
	"context"
	"fmt"
	"time"

	"github.com/kandev/kandev/pkg/pluginsdk"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const exactWorkflowPageLimit int32 = 100

func (h *pluginHost) ListWorkflowsExact(ctx context.Context, query pluginsdk.ExactWorkflowQuery) ([]pluginsdk.Workflow, *pluginsdk.ExactPageInfo, error) {
	if err := h.authorizeExactRead(query.WorkspaceID, query.CapabilityRevision, "host.v2.read:workflows", CanonicalApprovalDigest("workflows", query.WorkspaceID, query.Page.SnapshotVersion)); err != nil {
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
	parts := make([]string, 0, len(rows)*2)
	for i, row := range rows {
		if row == nil || row.WorkspaceID != query.WorkspaceID {
			return nil, nil, status.Error(codes.FailedPrecondition, "exact workflow projection is incomplete")
		}
		items[i] = workflowModelToDTO(row)
		parts = append(parts, row.ID, row.UpdatedAt.UTC().Format(time.RFC3339Nano))
	}
	return h.pageExactWorkflows(query.WorkspaceID, query.CapabilityRevision, "workflows", CanonicalApprovalDigest(parts...), query.Page, items)
}

func (h *pluginHost) ListWorkflowStepsExact(ctx context.Context, query pluginsdk.ExactWorkflowStepsQuery) ([]pluginsdk.WorkflowStep, *pluginsdk.ExactPageInfo, error) {
	if err := h.authorizeExactRead(query.WorkspaceID, query.CapabilityRevision, "host.v2.read:workflows", CanonicalApprovalDigest("workflow-steps", query.WorkspaceID, query.WorkflowID, query.Page.SnapshotVersion)); err != nil {
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
	items := make([]pluginsdk.WorkflowStep, len(rows))
	parts := make([]string, 0, len(rows)*5)
	for i, row := range rows {
		if row == nil || row.WorkflowID != query.WorkflowID {
			return nil, nil, status.Error(codes.FailedPrecondition, "exact workflow step projection is incomplete")
		}
		items[i] = workflowStepModelToDTO(row)
		parts = append(parts, row.ID, row.Name, fmt.Sprint(row.Position), string(row.StageType))
	}
	return h.pageExactWorkflowSteps(query.WorkspaceID, query.CapabilityRevision, "workflow-steps:"+query.WorkflowID, CanonicalApprovalDigest(parts...), query.Page, items)
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
