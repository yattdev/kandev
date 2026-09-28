package pluginsdk

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type decisionEvidenceRecordingHost struct {
	*recordingHost
	query ExactTaskDecisionEvidenceQuery
	page  *ExactTaskDecisionEvidencePage
	info  *ExactPageInfo
}

func (h *decisionEvidenceRecordingHost) ListTaskDecisionEvidenceExact(_ context.Context, query ExactTaskDecisionEvidenceQuery) (*ExactTaskDecisionEvidencePage, *ExactPageInfo, error) {
	h.query = query
	return h.page, h.info, nil
}

func TestHost_ListTaskDecisionEvidenceExactOverWire(t *testing.T) {
	impl := &decisionEvidenceRecordingHost{
		recordingHost: &recordingHost{},
		page: &ExactTaskDecisionEvidencePage{
			Relations:          []ExactTaskRelation{{TaskID: "task-1", BlockerTaskID: "task-2", WorkspaceID: "workspace-1", TaskResourceVersion: 3, BlockerResourceVersion: 4, ResourceVersion: 5}},
			PendingTransitions: []ExactPendingTaskTransition{{SessionID: "session-1", TaskID: "task-1", WorkspaceID: "workspace-1", SessionIncarnationID: "incarnation-1", WorkflowID: "workflow-1", WorkflowStepID: "step-1", StepPosition: 2, ResourceVersion: 6, TaskResourceVersion: 3, SessionResourceVersion: 7, QueueGeneration: 8, QueuedAt: "2026-09-28T10:00:00Z"}},
		},
		info: &ExactPageInfo{NextCursor: "cursor-2", HasMore: true, SnapshotVersion: "snapshot-1", AuditID: "audit-1"},
	}
	host := dialHostOverBufconn(t, impl)
	exact, ok := ExactTaskDecisionEvidence(host)
	require.True(t, ok)

	query := ExactTaskDecisionEvidenceQuery{WorkspaceID: "workspace-1", CapabilityRevision: 2, Page: ExactPage{Limit: 1, Cursor: "cursor-1", SnapshotVersion: "snapshot-1"}}
	page, info, err := exact.ListTaskDecisionEvidenceExact(context.Background(), query)
	require.NoError(t, err)
	require.Equal(t, query, impl.query)
	require.Equal(t, impl.page, page)
	require.Equal(t, impl.info, info)
}
