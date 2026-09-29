package pluginsdk

import (
	"context"
	"fmt"

	pluginv1 "github.com/kandev/kandev/proto/kandev/plugin/v1"
)

// ExactHostContractVersion identifies the additive exact Host contract. It
// does not change authority granted by any v1 Host method.
const ExactHostContractVersion = "2"

type CapabilityApprovalStatus string

const (
	CapabilityApprovalStatusActive  CapabilityApprovalStatus = "active"
	CapabilityApprovalStatusRevoked CapabilityApprovalStatus = "revoked"
)

// CapabilityApprovalContext is the current approval row visible on the
// connection-bound plugin installation. It is informational only and cannot
// be replayed as authority for an exact read or command.
type CapabilityApprovalContext struct {
	ApprovalID  string
	WorkspaceID string
	Revision    uint64
	Status      CapabilityApprovalStatus
}

// CapabilityContext is the Host-derived identity and approval state for one
// plugin broker connection.
type CapabilityContext struct {
	ContractVersion string
	InstallationID  string
	ManifestDigest  string
	Approvals       []CapabilityApprovalContext
}

// ExactHost is an optional Host extension. Keeping it separate preserves
// source compatibility for every existing v1 Host implementation.
type ExactHost interface {
	GetCapabilityContext(context.Context) (*CapabilityContext, error)
}

// ExactWorkspaceHost is an optional exact-read extension. It is deliberately
// separate from ExactHost so a Host that supports capability context does not
// accidentally advertise a workspace read it has not implemented.
type ExactWorkspaceHost interface {
	ListWorkspacesExact(context.Context, ExactWorkspaceQuery) ([]Workspace, *ExactPageInfo, error)
}

// ExactWorkflowHost exposes snapshot-bound workflow reads independently from
// the capability-context and workspace extensions.
type ExactWorkflowHost interface {
	ListWorkflowsExact(context.Context, ExactWorkflowQuery) ([]Workflow, *ExactPageInfo, error)
	ListWorkflowStepsExact(context.Context, ExactWorkflowStepsQuery) ([]WorkflowStep, *ExactPageInfo, error)
}

// ExactTaskHost exposes the narrow, snapshot-bound task projection. It does
// not widen the legacy Tasks reader or its api_read authority.
type ExactTaskHost interface {
	ListTasksExact(context.Context, ExactTaskQuery) ([]ExactTask, *ExactPageInfo, error)
	GetTaskExact(context.Context, ExactTaskGetQuery) (*ExactTask, error)
}

type ExactTaskCommandHost interface {
	UpdateTaskExact(context.Context, ExactTaskUpdateRequest) (*ExactTaskUpdateReceipt, error)
}

type ExactTaskUpdateRequest struct {
	WorkspaceID, TaskID, DecisionEvidenceSnapshotVersion string
	CapabilityRevision                                   uint64
	PendingTransition                                    ExactPendingTaskTransition
	Marker, IdempotencyKey                               string
	ExpectedResourceVersion                              int64
}
type ExactTaskUpdateReceipt struct {
	AuditID         string
	ResourceVersion int64
	// Outcome is pending when the task mutation committed but event delivery
	// has not been acknowledged. Retry the identical request to resolve it.
	Outcome ExactTaskUpdateOutcome
}

type ExactTaskUpdateOutcome string

const (
	ExactTaskUpdateDurable ExactTaskUpdateOutcome = "durable"
	ExactTaskUpdatePending ExactTaskUpdateOutcome = "pending"
)

// ExactSessionHost exposes the public-safe, snapshot-bound lifecycle
// projection for all sessions in one approved workspace.
type ExactSessionHost interface {
	ListSessionsExact(context.Context, ExactSessionQuery) ([]ExactSession, *ExactPageInfo, error)
	GetSessionExact(context.Context, ExactSessionGetQuery) (*ExactSession, error)
}

type ExactSessionMessageHost interface {
	ListSessionMessagesExact(context.Context, ExactSessionMessageQuery) ([]ExactSessionMessage, *ExactPageInfo, error)
}

type ExactTask struct {
	ID, WorkspaceID, WorkflowID, WorkflowStepID string
	Title, Description, State, Priority         string
	Position                                    int32
	Archived                                    bool
	ResourceVersion                             int64
}

type ExactTaskQuery struct {
	WorkspaceID        string
	CapabilityRevision uint64
	Page               ExactPage
}

type ExactTaskGetQuery struct {
	WorkspaceID, TaskID, SnapshotVersion string
	CapabilityRevision                   uint64
}

type ExactSessionQuery struct {
	WorkspaceID        string
	CapabilityRevision uint64
	Page               ExactPage
}
type ExactSessionGetQuery struct {
	WorkspaceID, SessionID, SnapshotVersion string
	CapabilityRevision                      uint64
}

type ExactSession struct {
	ID, TaskID, WorkspaceID, QueueIncarnationID string
	State                                       string
	RouteGeneration                             int64
	StartedAt, CompletedAt, UpdatedAt           string
	IsPrimary                                   bool
	ResourceVersion                             int64
}
type ExactSessionMessageQuery struct {
	WorkspaceID, TaskID, SessionID string
	CapabilityRevision             uint64
	Page                           ExactPage
}
type ExactSessionMessage struct {
	ID, AuthorType, Content, Type, CreatedAt, UpdatedAt string
	RequestsInput                                       bool
}

type ExactWorkspaceQuery struct {
	WorkspaceID        string
	CapabilityRevision uint64
	Page               ExactPage
}
type ExactPage struct {
	Limit                   int32
	Cursor, SnapshotVersion string
}
type ExactPageInfo struct {
	NextCursor      string
	HasMore         bool
	SnapshotVersion string
	AuditID         string
}

type ExactWorkflowQuery struct {
	WorkspaceID        string
	CapabilityRevision uint64
	Page               ExactPage
}

type ExactWorkflowStepsQuery struct {
	WorkspaceID        string
	WorkflowID         string
	CapabilityRevision uint64
	Page               ExactPage
}

// Exact returns the optional exact Host extension when the connected Host
// supports it.
func Exact(host Host) (ExactHost, bool) {
	exact, ok := host.(ExactHost)
	return exact, ok
}

// ExactWorkspaces returns the optional exact workspace-read extension.
func ExactWorkspaces(host Host) (ExactWorkspaceHost, bool) {
	exact, ok := host.(ExactWorkspaceHost)
	return exact, ok
}

func ExactWorkflows(host Host) (ExactWorkflowHost, bool) {
	exact, ok := host.(ExactWorkflowHost)
	return exact, ok
}

func ExactTasks(host Host) (ExactTaskHost, bool) {
	exact, ok := host.(ExactTaskHost)
	return exact, ok
}
func ExactTaskCommands(host Host) (ExactTaskCommandHost, bool) {
	exact, ok := host.(ExactTaskCommandHost)
	return exact, ok
}

func ExactSessions(host Host) (ExactSessionHost, bool) {
	exact, ok := host.(ExactSessionHost)
	return exact, ok
}
func ExactSessionMessages(host Host) (ExactSessionMessageHost, bool) {
	exact, ok := host.(ExactSessionMessageHost)
	return exact, ok
}

func capabilityContextToProto(in *CapabilityContext) *pluginv1.GetCapabilityContextResponse {
	if in == nil {
		return &pluginv1.GetCapabilityContextResponse{}
	}
	approvals := make([]*pluginv1.CapabilityApprovalContext, 0, len(in.Approvals))
	for _, approval := range in.Approvals {
		approvals = append(approvals, &pluginv1.CapabilityApprovalContext{
			ApprovalId: approval.ApprovalID, WorkspaceId: approval.WorkspaceID,
			Revision: approval.Revision, Status: string(approval.Status),
		})
	}
	return &pluginv1.GetCapabilityContextResponse{
		ContractVersion: in.ContractVersion, InstallationId: in.InstallationID,
		ManifestDigest: in.ManifestDigest, Approvals: approvals,
	}
}

func capabilityContextFromProto(in *pluginv1.GetCapabilityContextResponse) (*CapabilityContext, error) {
	if in == nil {
		return nil, fmt.Errorf("pluginsdk: capability context response is missing")
	}
	out := &CapabilityContext{
		ContractVersion: in.GetContractVersion(), InstallationID: in.GetInstallationId(),
		ManifestDigest: in.GetManifestDigest(), Approvals: make([]CapabilityApprovalContext, 0, len(in.GetApprovals())),
	}
	if out.ContractVersion != ExactHostContractVersion || out.InstallationID == "" || out.ManifestDigest == "" {
		return nil, fmt.Errorf("pluginsdk: capability context is incomplete")
	}
	for _, approval := range in.GetApprovals() {
		status := CapabilityApprovalStatus(approval.GetStatus())
		if approval.GetApprovalId() == "" || approval.GetWorkspaceId() == "" || approval.GetRevision() == 0 ||
			(status != CapabilityApprovalStatusActive && status != CapabilityApprovalStatusRevoked) {
			return nil, fmt.Errorf("pluginsdk: capability approval context is malformed")
		}
		out.Approvals = append(out.Approvals, CapabilityApprovalContext{
			ApprovalID: approval.GetApprovalId(), WorkspaceID: approval.GetWorkspaceId(),
			Revision: approval.GetRevision(), Status: status,
		})
	}
	return out, nil
}

// ExactTaskDecisionEvidenceHost exposes one atomic relation and pending-move
// projection. It is separate from task reads because it requires the shared
// SQLite authority boundary.
type ExactTaskDecisionEvidenceHost interface {
	ListTaskDecisionEvidenceExact(context.Context, ExactTaskDecisionEvidenceQuery) (*ExactTaskDecisionEvidencePage, *ExactPageInfo, error)
}

type ExactTaskRelation struct {
	TaskID, BlockerTaskID, WorkspaceID                           string
	TaskResourceVersion, BlockerResourceVersion, ResourceVersion int64
}

type ExactPendingTaskTransition struct {
	SessionID, TaskID, WorkspaceID, SessionIncarnationID string
	WorkflowID, WorkflowStepID                           string
	StepPosition                                         int32
	ResourceVersion, TaskResourceVersion                 int64
	SessionResourceVersion, QueueGeneration              int64
	QueuedAt                                             string
}

type ExactTaskDecisionEvidenceQuery struct {
	WorkspaceID        string
	CapabilityRevision uint64
	Page               ExactPage
}

type ExactTaskDecisionEvidencePage struct {
	Relations          []ExactTaskRelation
	PendingTransitions []ExactPendingTaskTransition
}

func ExactTaskDecisionEvidence(host Host) (ExactTaskDecisionEvidenceHost, bool) {
	exact, ok := host.(ExactTaskDecisionEvidenceHost)
	return exact, ok
}
