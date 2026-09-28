package plugins

import (
	"context"
	"time"

	"github.com/kandev/kandev/internal/exactsnapshotcomposite"
	"github.com/kandev/kandev/pkg/pluginsdk"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const exactDecisionEvidencePageLimit int32 = 99

type exactDecisionEvidenceSource interface {
	Open(context.Context, exactsnapshotcomposite.Request) (*exactsnapshotcomposite.Snapshot, error)
	Page(context.Context, string, int, int, int, int) (*exactsnapshotcomposite.Page, error)
}

func (h *pluginHost) ListTaskDecisionEvidenceExact(ctx context.Context, query pluginsdk.ExactTaskDecisionEvidenceQuery) (*pluginsdk.ExactTaskDecisionEvidencePage, *pluginsdk.ExactPageInfo, error) {
	receipt, err := h.authorizeExactReadReceipt(query.WorkspaceID, query.CapabilityRevision, "host.v2.read:tasks", CanonicalApprovalDigest("task-decision-evidence", query.WorkspaceID, query.Page.SnapshotVersion))
	if err != nil {
		return nil, nil, err
	}
	evidence, err := h.exactDecisionEvidenceReader()
	if err != nil {
		return nil, nil, resourceExhausted("exact task decision evidence is unavailable")
	}
	binding, err := h.exactDecisionEvidenceBinding(ctx, evidence, query)
	if err != nil {
		return nil, nil, err
	}
	offset, err := h.exactTaskOffset(binding, query.Page)
	if err != nil {
		return nil, nil, err
	}
	limit := exactDecisionEvidencePageLimit
	if query.Page.Limit > 0 {
		limit = query.Page.Limit
	}
	page, err := evidence.Page(ctx, binding.ProjectionVersion, offset, int(limit)+1, offset, int(limit)+1)
	if err != nil {
		return nil, nil, status.Error(codes.FailedPrecondition, "exact task decision evidence is unavailable")
	}
	result, err := exactDecisionEvidencePageToDTO(page, query.WorkspaceID, limit)
	if err != nil {
		return nil, nil, err
	}
	snapshotVersion, err := h.exactSnapshots.create(binding, 0)
	if err != nil {
		return nil, nil, status.Error(codes.Unavailable, "exact task decision evidence is unavailable")
	}
	info := &pluginsdk.ExactPageInfo{SnapshotVersion: snapshotVersion, HasMore: len(page.Relations) > len(result.Relations) || len(page.PendingTransitions) > len(result.PendingTransitions), AuditID: receipt.AuditID}
	if info.HasMore {
		cursor, err := h.exactSnapshots.create(binding, offset+int(limit))
		if err != nil {
			return nil, nil, status.Error(codes.Unavailable, "exact task decision evidence cursor is unavailable")
		}
		info.NextCursor = cursor
	}
	return result, info, nil
}

func (h *pluginHost) exactDecisionEvidenceReader() (exactDecisionEvidenceSource, error) {
	evidence := h.exactDecisionEvidence
	if h.exactDecisionEvidenceDep != nil {
		evidence = h.exactDecisionEvidenceDep()
	}
	if evidence == nil || h.exactSnapshots == nil {
		return nil, status.Error(codes.ResourceExhausted, "exact task decision evidence is unavailable")
	}
	return evidence, nil
}

func exactDecisionEvidencePageToDTO(page *exactsnapshotcomposite.Page, workspaceID string, limit int32) (*pluginsdk.ExactTaskDecisionEvidencePage, error) {
	result := &pluginsdk.ExactTaskDecisionEvidencePage{Relations: make([]pluginsdk.ExactTaskRelation, min(len(page.Relations), int(limit))), PendingTransitions: make([]pluginsdk.ExactPendingTaskTransition, min(len(page.PendingTransitions), int(limit)))}
	for i := range result.Relations {
		r := page.Relations[i]
		if r.WorkspaceID != workspaceID {
			return nil, status.Error(codes.FailedPrecondition, "exact task decision evidence is incomplete")
		}
		result.Relations[i] = pluginsdk.ExactTaskRelation{TaskID: r.TaskID, BlockerTaskID: r.BlockerTaskID, WorkspaceID: r.WorkspaceID, TaskResourceVersion: r.TaskResourceVersion, BlockerResourceVersion: r.BlockerResourceVersion, ResourceVersion: r.ResourceVersion}
	}
	for i := range result.PendingTransitions {
		p := page.PendingTransitions[i]
		if p.WorkspaceID != workspaceID {
			return nil, status.Error(codes.FailedPrecondition, "exact task decision evidence is incomplete")
		}
		result.PendingTransitions[i] = pluginsdk.ExactPendingTaskTransition{SessionID: p.SessionID, TaskID: p.TaskID, WorkspaceID: p.WorkspaceID, SessionIncarnationID: p.SessionIncarnationID, WorkflowID: p.WorkflowID, WorkflowStepID: p.WorkflowStepID, StepPosition: int32(p.Position), ResourceVersion: p.ResourceVersion, TaskResourceVersion: p.TaskResourceVersion, SessionResourceVersion: p.SessionResourceVersion, QueueGeneration: p.QueueGeneration, QueuedAt: p.QueuedAt.UTC().Format(time.RFC3339Nano)}
	}
	return result, nil
}

func (h *pluginHost) exactDecisionEvidenceBinding(ctx context.Context, evidence exactDecisionEvidenceSource, query pluginsdk.ExactTaskDecisionEvidenceQuery) (exactSnapshotBinding, error) {
	if query.Page.Limit < 0 || query.Page.Limit > exactDecisionEvidencePageLimit || query.Page.Cursor != "" && query.Page.SnapshotVersion == "" {
		return exactSnapshotBinding{}, status.Error(codes.InvalidArgument, "exact snapshot page is invalid")
	}
	if query.Page.SnapshotVersion != "" {
		return h.exactDecisionEvidenceSnapshotBinding(query.WorkspaceID, query.CapabilityRevision, query.Page.SnapshotVersion)
	}
	snapshot, err := evidence.Open(ctx, exactsnapshotcomposite.Request{WorkspaceID: query.WorkspaceID})
	if err != nil || snapshot == nil || snapshot.Token == "" || snapshot.WorkspaceID != query.WorkspaceID {
		return exactSnapshotBinding{}, status.Error(codes.FailedPrecondition, "exact task decision evidence is unavailable")
	}
	return h.exactPageBinding(query.WorkspaceID, query.CapabilityRevision, "task-decision-evidence", snapshot.Token), nil
}
func (h *pluginHost) exactDecisionEvidenceSnapshotBinding(workspaceID string, revision uint64, snapshotVersion string) (exactSnapshotBinding, error) {
	cursor, err := h.exactSnapshots.parse(snapshotVersion)
	want := h.exactPageBinding(workspaceID, revision, "task-decision-evidence", cursor.ProjectionVersion)
	if err != nil || cursor.Offset != 0 || cursor.exactSnapshotBinding != want {
		return exactSnapshotBinding{}, status.Error(codes.InvalidArgument, "exact task decision evidence snapshot is invalid")
	}
	return cursor.exactSnapshotBinding, nil
}

var _ pluginsdk.ExactTaskDecisionEvidenceHost = (*pluginHost)(nil)
