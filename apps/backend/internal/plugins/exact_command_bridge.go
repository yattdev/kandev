package plugins

import (
	"context"
	"fmt"

	"github.com/kandev/kandev/internal/exactsnapshotauthority"
	"github.com/kandev/kandev/internal/exactsnapshotcomposite"
	"github.com/kandev/kandev/internal/orchestrator/messagequeue"
	tasksqlite "github.com/kandev/kandev/internal/task/repository/sqlite"
)

// ExactTaskCommandApprovalBridge is the only path by which H6 approvals and
// receipts reach the SQLite exact-command authority. A nil bridge leaves the
// legacy approval ledger usable but cannot enable an exact command.
type ExactTaskCommandApprovalBridge interface {
	Grant(context.Context, CapabilityApproval, string) error
	Revoke(context.Context, CapabilityApproval, string) error
	RevokeWorkspace(context.Context, string, string) error
	RevokeInstallation(context.Context, string) error
	RecordReceipt(context.Context, ApprovalReceipt) error
}

type sqliteExactTaskCommandApprovalBridge struct{ repo *tasksqlite.Repository }

// ExactTaskCommandGrantIssuer is private Host composition. It accepts only a
// composite evidence token and complete observed pending row, then creates the
// SQLite grant in that evidence transaction. No RPC or SDK method exposes it.
type ExactTaskCommandGrantIssuer interface {
	Issue(context.Context, tasksqlite.ExactTaskCommandGrant, string, messagequeue.ExactPendingTransition) error
}

type sqliteExactTaskCommandGrantIssuer struct {
	repo     *tasksqlite.Repository
	evidence *exactsnapshotcomposite.Repository
}

func NewSQLiteExactTaskCommandApprovalBridge(repo *tasksqlite.Repository) (ExactTaskCommandApprovalBridge, error) {
	if repo == nil || !repo.ExactTaskCommandAvailable() {
		return nil, fmt.Errorf("plugins: exact command repository is required")
	}
	return sqliteExactTaskCommandApprovalBridge{repo: repo}, nil
}

// NewSQLiteExactTaskCommandGrantIssuer binds the exact command journal to the
// same composite evidence authority used by the plugin Host. PostgreSQL and
// unavailable repositories fail closed through the existing bridge check.
func NewSQLiteExactTaskCommandGrantIssuer(repo *tasksqlite.Repository, evidence *exactsnapshotcomposite.Repository) (ExactTaskCommandGrantIssuer, error) {
	if _, err := NewSQLiteExactTaskCommandApprovalBridge(repo); err != nil || evidence == nil {
		return nil, fmt.Errorf("plugins: exact command grant issuer is required")
	}
	return sqliteExactTaskCommandGrantIssuer{repo: repo, evidence: evidence}, nil
}

func (i sqliteExactTaskCommandGrantIssuer) Issue(ctx context.Context, grant tasksqlite.ExactTaskCommandGrant, compositeSnapshotToken string, observed messagequeue.ExactPendingTransition) error {
	if i.repo == nil || i.evidence == nil {
		return tasksqlite.ErrExactTaskCommandUnavailable
	}
	return i.evidence.WithPendingTransitionAuthority(ctx, compositeSnapshotToken, observed, func(ctx context.Context, authority *exactsnapshotauthority.Authority, tx *exactsnapshotauthority.Transaction, pendingSnapshotToken string) error {
		return i.repo.IssueExactTaskCommandGrantInAuthorityTx(ctx, authority, tx, grant, pendingSnapshotToken, observed)
	})
}

func (b sqliteExactTaskCommandApprovalBridge) Grant(ctx context.Context, approval CapabilityApproval, auditID string) error {
	for _, capabilityID := range approval.CapabilityIDs {
		if err := b.repo.UpsertExactTaskCommandApproval(ctx, tasksqlite.ExactTaskCommandApproval{InstallationID: approval.InstallationID, WorkspaceID: approval.WorkspaceID, CapabilityID: capabilityID, ReceiptAuditID: auditID, Revision: approval.Revision}); err != nil {
			return err
		}
	}
	return nil
}
func (b sqliteExactTaskCommandApprovalBridge) Revoke(ctx context.Context, approval CapabilityApproval, _ string) error {
	return b.repo.RevokeExactTaskCommandWorkspace(ctx, approval.InstallationID, approval.WorkspaceID)
}
func (b sqliteExactTaskCommandApprovalBridge) RevokeInstallation(ctx context.Context, installationID string) error {
	return b.repo.RevokeExactTaskCommandInstallation(ctx, installationID)
}
func (b sqliteExactTaskCommandApprovalBridge) RevokeWorkspace(ctx context.Context, installationID, workspaceID string) error {
	return b.repo.RevokeExactTaskCommandWorkspace(ctx, installationID, workspaceID)
}
func (b sqliteExactTaskCommandApprovalBridge) RecordReceipt(ctx context.Context, receipt ApprovalReceipt) error {
	return b.repo.RecordExactTaskCommandReceipt(ctx, tasksqlite.ExactTaskCommandApproval{InstallationID: receipt.InstallationID, WorkspaceID: receipt.WorkspaceID, CapabilityID: receipt.CapabilityID, ReceiptAuditID: receipt.AuditID, Revision: receipt.Revision}, receipt.ObservedAt)
}

// SetExactTaskCommandApprovalBridge wires a same-writer exact authority. It
// is intentionally separate from SetDataSources: Host writes remain disabled.
func (s *Service) SetExactTaskCommandApprovalBridge(bridge ExactTaskCommandApprovalBridge) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.exactCommandApprovals = bridge
}
func (s *Service) exactTaskCommandApprovalBridge() ExactTaskCommandApprovalBridge {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exactCommandApprovals
}

// ExactTaskCommandAuthorityAvailable reports whether startup bound this
// service to a same-writer exact-command authority. It discloses no grant or
// approval state and is used by composition health checks.
func (s *Service) ExactTaskCommandAuthorityAvailable() bool {
	return s.exactTaskCommandApprovalBridge() != nil
}

// SetExactTaskCommandGrantIssuer attaches the unadvertised Host grant path
// after the queue and composite evidence reader are available.
func (s *Service) SetExactTaskCommandGrantIssuer(issuer ExactTaskCommandGrantIssuer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.exactTaskCommandGrantIssuer = issuer
}

func (s *Service) exactTaskCommandGrantIssuerDep() ExactTaskCommandGrantIssuer {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exactTaskCommandGrantIssuer
}
