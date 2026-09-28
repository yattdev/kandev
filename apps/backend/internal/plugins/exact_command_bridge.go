package plugins

import (
	"context"
	"fmt"

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

func NewSQLiteExactTaskCommandApprovalBridge(repo *tasksqlite.Repository) (ExactTaskCommandApprovalBridge, error) {
	if repo == nil || !repo.ExactTaskCommandAvailable() {
		return nil, fmt.Errorf("plugins: exact command repository is required")
	}
	return sqliteExactTaskCommandApprovalBridge{repo: repo}, nil
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
