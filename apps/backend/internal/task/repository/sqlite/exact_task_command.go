package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/kandev/kandev/internal/db/dialect"
	"github.com/kandev/kandev/internal/exactsnapshotauthority"
	"github.com/kandev/kandev/internal/orchestrator/messagequeue"
)

// ErrExactTaskCommandUnavailable is the fail-closed result for an exact
// command whose approval, grant, task version, or workspace fence changed.
var ErrExactTaskCommandUnavailable = errors.New("exact task command unavailable")

const exactTaskDescriptionCommandCapability = "host.v2.write:tasks"

func (r *Repository) ExactTaskCommandAvailable() bool {
	return r != nil && r.db != nil && !dialect.IsPostgres(r.db.DriverName())
}

// ExactTaskCommandWorkspaceFence returns the current task-writer fence for
// an internal exact command precondition.
func (r *Repository) ExactTaskCommandWorkspaceFence(ctx context.Context, workspaceID string) (int64, error) {
	if !r.ExactTaskCommandAvailable() || workspaceID == "" {
		return 0, ErrExactTaskCommandUnavailable
	}
	var revision int64
	if err := r.db.QueryRowxContext(ctx, r.db.Rebind(`SELECT revision FROM exact_task_workspace_fences WHERE workspace_id = ?`), workspaceID).Scan(&revision); err != nil {
		return 0, ErrExactTaskCommandUnavailable
	}
	return revision, nil
}

// ExactTaskCommandApproval is the SQLite-owned approval projection used only
// by the future exact command path. Legacy approvals.json is deliberately not
// consulted here: a command can be enabled only after a Host bridge makes the
// approval and its revocation durable on this writer.
type ExactTaskCommandApproval struct {
	InstallationID, WorkspaceID, CapabilityID, ReceiptAuditID string
	Revision                                                  uint64
}

// ExactTaskCommandGrant is a server-owned, one-shot authorization record.
type ExactTaskCommandGrant struct {
	ID, InstallationID, WorkspaceID, TaskID, CapabilityID string
	ReceiptAuditID, ActionDigest, IdempotencyKey          string
	ApprovalRevision                                      uint64
	ExpiresAt                                             time.Time
}

// ExactTaskDescriptionCommand is intentionally repository-internal plumbing;
// it is not exposed through the plugin Host or SDK.
type ExactTaskDescriptionCommand struct {
	GrantID, InstallationID, WorkspaceID, TaskID string
	CapabilityID, ReceiptAuditID                 string
	ApprovalRevision                             uint64
	ActionDigest, IdempotencyKey, Marker         string
	ExpectedResourceVersion, ExpectedFence       int64
	PendingSnapshotToken                         string
	PendingTransition                            *messagequeue.ExactPendingTransition
}

type ExactTaskDescriptionReceipt struct {
	AuditID         string
	ResourceVersion int64
}

type exactTaskCommandTransaction struct {
	tx               *sqlx.Tx
	commit, rollback func() error
	validator        messagequeue.ExactPendingTransitionAuthorityReader
	authority        *exactsnapshotauthority.Authority
	authorityTx      *exactsnapshotauthority.Transaction
}

// SetExactTaskCommandPendingValidator installs the SQLite queue validator
// used only by the unadvertised exact command path.
func (r *Repository) SetExactTaskCommandPendingValidator(validator messagequeue.ExactPendingTransitionAuthorityReader) {
	r.exactTaskCommandPendingValidator = validator
}

func (r *Repository) initExactTaskCommandSchema() error {
	if err := r.migrate.Apply("exact_task_commands.tables", `
		CREATE TABLE IF NOT EXISTS exact_task_command_approvals (
			installation_id TEXT NOT NULL, workspace_id TEXT NOT NULL,
			capability_id TEXT NOT NULL, receipt_audit_id TEXT NOT NULL,
			revision BIGINT NOT NULL, revoked_at TIMESTAMP,
			PRIMARY KEY (installation_id, workspace_id, capability_id)
		);
		CREATE TABLE IF NOT EXISTS exact_task_command_grants (
			id TEXT PRIMARY KEY, installation_id TEXT NOT NULL, workspace_id TEXT NOT NULL,
			task_id TEXT NOT NULL, capability_id TEXT NOT NULL, receipt_audit_id TEXT NOT NULL,
			approval_revision BIGINT NOT NULL, action_digest TEXT NOT NULL,
			idempotency_key TEXT NOT NULL, expires_at TIMESTAMP NOT NULL, consumed_at TIMESTAMP,
			revoked_at TIMESTAMP, UNIQUE (installation_id, workspace_id, idempotency_key)
		);
		CREATE TABLE IF NOT EXISTS exact_task_command_audits (
			audit_id TEXT PRIMARY KEY, installation_id TEXT NOT NULL, workspace_id TEXT NOT NULL,
			task_id TEXT NOT NULL, action_digest TEXT NOT NULL, idempotency_key TEXT NOT NULL,
			resource_version BIGINT NOT NULL, created_at TIMESTAMP NOT NULL,
		UNIQUE (installation_id, workspace_id, idempotency_key)
		);
		CREATE TABLE IF NOT EXISTS exact_task_command_receipts (
			audit_id TEXT PRIMARY KEY, installation_id TEXT NOT NULL, workspace_id TEXT NOT NULL,
			capability_id TEXT NOT NULL, approval_revision BIGINT NOT NULL, observed_at TIMESTAMP NOT NULL,
		UNIQUE (installation_id, workspace_id, capability_id, audit_id)
		);`); err != nil {
		return err
	}
	return r.migrate.Apply("exact_task_commands.audit_identity", `ALTER TABLE exact_task_command_audits ADD COLUMN command_identity TEXT NOT NULL DEFAULT ''`)
}

// RecordExactTaskCommandReceipt binds an H6 decision receipt to the active
// SQLite approval projection. A missing projection is denied rather than
// falling back to approvals.json.
func (r *Repository) RecordExactTaskCommandReceipt(ctx context.Context, approval ExactTaskCommandApproval, observedAt time.Time) error {
	if !validExactCommandApproval(approval) || observedAt.IsZero() {
		return ErrExactTaskCommandUnavailable
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = requireExactCommandApproval(ctx, r, tx, approval.InstallationID, approval.WorkspaceID, approval.CapabilityID, approval.Revision); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, r.db.Rebind(`INSERT INTO exact_task_command_receipts(audit_id, installation_id, workspace_id, capability_id, approval_revision, observed_at) VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(audit_id) DO NOTHING`), approval.ReceiptAuditID, approval.InstallationID, approval.WorkspaceID, approval.CapabilityID, approval.Revision, observedAt.UTC())
	if err != nil {
		return err
	}
	return tx.Commit()
}

// UpsertExactTaskCommandApproval is the narrow future bridge from an H6
// receipt. It accepts only monotonic revisions and performs no JSON fallback.
func (r *Repository) UpsertExactTaskCommandApproval(ctx context.Context, approval ExactTaskCommandApproval) error {
	if !validExactCommandApproval(approval) {
		return ErrExactTaskCommandUnavailable
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var revision uint64
	var revokedAt sql.NullTime
	err = tx.QueryRowxContext(ctx, r.db.Rebind(`SELECT revision, revoked_at FROM exact_task_command_approvals WHERE installation_id = ? AND workspace_id = ? AND capability_id = ?`), approval.InstallationID, approval.WorkspaceID, approval.CapabilityID).Scan(&revision, &revokedAt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && ((!revokedAt.Valid && approval.Revision != revision+1) || (revokedAt.Valid && approval.Revision < revision)) {
		return ErrExactTaskCommandUnavailable
	}
	if err != nil && approval.Revision != 1 {
		return ErrExactTaskCommandUnavailable
	}
	_, err = tx.ExecContext(ctx, r.db.Rebind(`INSERT INTO exact_task_command_approvals(installation_id, workspace_id, capability_id, receipt_audit_id, revision, revoked_at) VALUES (?, ?, ?, ?, ?, NULL) ON CONFLICT(installation_id, workspace_id, capability_id) DO UPDATE SET receipt_audit_id = excluded.receipt_audit_id, revision = excluded.revision, revoked_at = NULL`), approval.InstallationID, approval.WorkspaceID, approval.CapabilityID, approval.ReceiptAuditID, approval.Revision)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// RevokeExactTaskCommandApproval revokes the exact projection and every live
// matching grant in the same writer transaction.
func (r *Repository) RevokeExactTaskCommandApproval(ctx context.Context, approval ExactTaskCommandApproval) error {
	if !validExactCommandApproval(approval) {
		return ErrExactTaskCommandUnavailable
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, r.db.Rebind(`UPDATE exact_task_command_approvals SET revision = ?, receipt_audit_id = ?, revoked_at = ? WHERE installation_id = ? AND workspace_id = ? AND capability_id = ? AND revision = ? AND revoked_at IS NULL`), approval.Revision+1, approval.ReceiptAuditID, r.nowUTC(), approval.InstallationID, approval.WorkspaceID, approval.CapabilityID, approval.Revision)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return ErrExactTaskCommandUnavailable
	}
	if _, err = tx.ExecContext(ctx, r.db.Rebind(`UPDATE exact_task_command_grants SET revoked_at = ? WHERE installation_id = ? AND workspace_id = ? AND capability_id = ? AND approval_revision = ? AND consumed_at IS NULL AND revoked_at IS NULL`), r.nowUTC(), approval.InstallationID, approval.WorkspaceID, approval.CapabilityID, approval.Revision); err != nil {
		return err
	}
	return tx.Commit()
}

// RevokeExactTaskCommandInstallation closes every exact command approval for
// an installation before its manifest review or uninstall changes the H6 row.
func (r *Repository) RevokeExactTaskCommandInstallation(ctx context.Context, installationID string) error {
	if !r.ExactTaskCommandAvailable() || installationID == "" {
		return ErrExactTaskCommandUnavailable
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	now := r.nowUTC()
	if _, err = tx.ExecContext(ctx, r.db.Rebind(`UPDATE exact_task_command_approvals SET revision = revision + 1, revoked_at = ? WHERE installation_id = ? AND revoked_at IS NULL`), now, installationID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, r.db.Rebind(`UPDATE exact_task_command_grants SET revoked_at = ? WHERE installation_id = ? AND consumed_at IS NULL AND revoked_at IS NULL`), now, installationID); err != nil {
		return err
	}
	return tx.Commit()
}

// RevokeExactTaskCommandWorkspace closes old grants before a workspace
// approval is replaced or narrowed, without affecting other workspaces.
func (r *Repository) RevokeExactTaskCommandWorkspace(ctx context.Context, installationID, workspaceID string) error {
	if !r.ExactTaskCommandAvailable() || installationID == "" || workspaceID == "" {
		return ErrExactTaskCommandUnavailable
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	now := r.nowUTC()
	if _, err = tx.ExecContext(ctx, r.db.Rebind(`UPDATE exact_task_command_approvals SET revision = revision + 1, revoked_at = ? WHERE installation_id = ? AND workspace_id = ? AND revoked_at IS NULL`), now, installationID, workspaceID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, r.db.Rebind(`UPDATE exact_task_command_grants SET revoked_at = ? WHERE installation_id = ? AND workspace_id = ? AND consumed_at IS NULL AND revoked_at IS NULL`), now, installationID, workspaceID); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *Repository) IssueExactTaskCommandGrant(ctx context.Context, grant ExactTaskCommandGrant) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = r.issueExactTaskCommandGrantInTx(ctx, tx, grant); err != nil {
		return err
	}
	return tx.Commit()
}

// issueExactTaskCommandGrantInAuthorityTx creates a grant only after the
// queue-owned evidence validator has accepted its observed pending row in the
// same sealed SQLite transaction. It intentionally leaves resolution of that
// transaction to the authority owner.
func (r *Repository) issueExactTaskCommandGrantInAuthorityTx(ctx context.Context, authority *exactsnapshotauthority.Authority, tx *exactsnapshotauthority.Transaction, grant ExactTaskCommandGrant, snapshotToken string, observed messagequeue.ExactPendingTransition) error {
	if !r.ExactTaskCommandAvailable() || r.exactTaskCommandPendingValidator == nil || authority == nil || !authority.Matches(r.db) || tx == nil || !tx.Matches(authority) || snapshotToken == "" || observed.WorkspaceID != grant.WorkspaceID || observed.TaskID != grant.TaskID {
		return ErrExactTaskCommandUnavailable
	}
	if err := r.exactTaskCommandPendingValidator.ValidateExactPendingTransitionInAuthorityTx(ctx, authority, tx, snapshotToken, observed); err != nil {
		return ErrExactTaskCommandUnavailable
	}
	return r.issueExactTaskCommandGrantInTx(ctx, tx.SQLX(), grant)
}

// IssueExactTaskCommandGrantInAuthorityTx is the unadvertised composition
// seam for a Host-owned grant issuer. The transaction and authority can only
// come from the matching SQLite queue evidence reader; callers cannot use it
// to create a grant outside the observed pending-transition fence.
func (r *Repository) IssueExactTaskCommandGrantInAuthorityTx(ctx context.Context, authority *exactsnapshotauthority.Authority, tx *exactsnapshotauthority.Transaction, grant ExactTaskCommandGrant, snapshotToken string, observed messagequeue.ExactPendingTransition) error {
	return r.issueExactTaskCommandGrantInAuthorityTx(ctx, authority, tx, grant, snapshotToken, observed)
}

func (r *Repository) issueExactTaskCommandGrantInTx(ctx context.Context, tx *sqlx.Tx, grant ExactTaskCommandGrant) error {
	if !r.ExactTaskCommandAvailable() || tx == nil || !validExactCommandGrant(grant) || grant.CapabilityID != exactTaskDescriptionCommandCapability || !grant.ExpiresAt.After(r.nowUTC()) {
		return ErrExactTaskCommandUnavailable
	}
	if err := requireExactCommandApproval(ctx, r, tx, grant.InstallationID, grant.WorkspaceID, grant.CapabilityID, grant.ApprovalRevision); err != nil {
		return err
	}
	if err := requireExactCommandReceipt(ctx, r, tx, grant.InstallationID, grant.WorkspaceID, grant.CapabilityID, grant.ReceiptAuditID, grant.ApprovalRevision); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, r.db.Rebind(`INSERT INTO exact_task_command_grants(id, installation_id, workspace_id, task_id, capability_id, receipt_audit_id, approval_revision, action_digest, idempotency_key, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`), grant.ID, grant.InstallationID, grant.WorkspaceID, grant.TaskID, grant.CapabilityID, grant.ReceiptAuditID, grant.ApprovalRevision, grant.ActionDigest, grant.IdempotencyKey, grant.ExpiresAt.UTC())
	if err != nil {
		return fmt.Errorf("issue exact task command grant: %w", err)
	}
	return nil
}

// ApplyExactTaskDescriptionCommand is intentionally unadvertised. It proves
// the one-transaction prerequisite only; wiring it to UpdateTaskExact waits
// for the Host approval bridge and independent contract gates.
func (r *Repository) ApplyExactTaskDescriptionCommand(ctx context.Context, command ExactTaskDescriptionCommand) (ExactTaskDescriptionReceipt, error) {
	if !validExactCommand(command) {
		return ExactTaskDescriptionReceipt{}, ErrExactTaskCommandUnavailable
	}
	commandTx, err := r.beginExactTaskCommandTransaction(ctx, command)
	if err != nil {
		return ExactTaskDescriptionReceipt{}, err
	}
	defer func() { _ = commandTx.rollback() }()
	if receipt, replayed, replayErr := exactTaskCommandReplay(ctx, r, commandTx.tx, command); replayed {
		if replayErr != nil {
			return ExactTaskDescriptionReceipt{}, replayErr
		}
		return receipt, commandTx.commit()
	}
	if err = commandTx.validatePending(ctx, command); err != nil {
		return ExactTaskDescriptionReceipt{}, err
	}
	tx := commandTx.tx
	if err = requireExactCommandApproval(ctx, r, tx, command.InstallationID, command.WorkspaceID, command.CapabilityID, command.ApprovalRevision); err != nil {
		return ExactTaskDescriptionReceipt{}, err
	}
	if err = requireExactCommandReceipt(ctx, r, tx, command.InstallationID, command.WorkspaceID, command.CapabilityID, command.ReceiptAuditID, command.ApprovalRevision); err != nil {
		return ExactTaskDescriptionReceipt{}, err
	}
	now := r.nowUTC()
	if err = r.consumeExactTaskCommandGrant(ctx, tx, command, now); err != nil {
		return ExactTaskDescriptionReceipt{}, err
	}
	result, err := tx.ExecContext(ctx, r.db.Rebind(`UPDATE tasks SET description = ?, updated_at = ? WHERE id = ? AND workspace_id = ? AND resource_version = ? AND (SELECT revision FROM exact_task_workspace_fences WHERE workspace_id = ?) = ?`), command.Marker, now, command.TaskID, command.WorkspaceID, command.ExpectedResourceVersion, command.WorkspaceID, command.ExpectedFence)
	if err != nil {
		return ExactTaskDescriptionReceipt{}, err
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return ExactTaskDescriptionReceipt{}, ErrExactTaskCommandUnavailable
	}
	var version int64
	if err = tx.QueryRowxContext(ctx, r.db.Rebind(`SELECT resource_version FROM tasks WHERE id = ?`), command.TaskID).Scan(&version); err != nil {
		return ExactTaskDescriptionReceipt{}, err
	}
	if err = r.afterExactTaskCommandCAS(); err != nil {
		return ExactTaskDescriptionReceipt{}, err
	}
	if _, err = tx.ExecContext(ctx, r.db.Rebind(`INSERT INTO exact_task_command_audits(audit_id, installation_id, workspace_id, task_id, action_digest, idempotency_key, resource_version, command_identity, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`), command.IdempotencyKey, command.InstallationID, command.WorkspaceID, command.TaskID, command.ActionDigest, command.IdempotencyKey, version, exactTaskCommandIdentity(command), now); err != nil {
		return ExactTaskDescriptionReceipt{}, err
	}
	if err = commandTx.commit(); err != nil {
		return ExactTaskDescriptionReceipt{}, err
	}
	return ExactTaskDescriptionReceipt{AuditID: command.IdempotencyKey, ResourceVersion: version}, nil
}

func (r *Repository) afterExactTaskCommandCAS() error {
	if r.exactTaskCommandBeforeAudit == nil {
		return nil
	}
	return r.exactTaskCommandBeforeAudit()
}

func (r *Repository) beginExactTaskCommandTransaction(ctx context.Context, command ExactTaskDescriptionCommand) (*exactTaskCommandTransaction, error) {
	if r.exactTaskCommandPendingValidator == nil {
		tx, err := r.db.BeginTxx(ctx, nil)
		if err != nil {
			return nil, err
		}
		return &exactTaskCommandTransaction{tx: tx, commit: tx.Commit, rollback: tx.Rollback}, nil
	}
	if command.PendingTransition == nil || command.PendingSnapshotToken == "" {
		return nil, ErrExactTaskCommandUnavailable
	}
	authority, err := exactsnapshotauthority.NewSQLite(r.db)
	if err != nil {
		return nil, ErrExactTaskCommandUnavailable
	}
	authorityTx, err := r.exactTaskCommandPendingValidator.BeginExactPendingTransitionSnapshotAuthorityTx(ctx, authority)
	if err != nil {
		return nil, ErrExactTaskCommandUnavailable
	}
	return &exactTaskCommandTransaction{tx: authorityTx.SQLX(), commit: authorityTx.Commit, rollback: authorityTx.Rollback, validator: r.exactTaskCommandPendingValidator, authority: authority, authorityTx: authorityTx}, nil
}

func (t *exactTaskCommandTransaction) validatePending(ctx context.Context, command ExactTaskDescriptionCommand) error {
	if t.validator == nil {
		return nil
	}
	if command.PendingTransition.WorkspaceID != command.WorkspaceID || command.PendingTransition.TaskID != command.TaskID || t.validator.ValidateExactPendingTransitionInAuthorityTx(ctx, t.authority, t.authorityTx, command.PendingSnapshotToken, *command.PendingTransition) != nil {
		return ErrExactTaskCommandUnavailable
	}
	return nil
}

func (r *Repository) consumeExactTaskCommandGrant(ctx context.Context, tx *sqlx.Tx, command ExactTaskDescriptionCommand, now time.Time) error {
	result, err := tx.ExecContext(ctx, r.db.Rebind(`UPDATE exact_task_command_grants SET consumed_at = ? WHERE id = ? AND installation_id = ? AND workspace_id = ? AND task_id = ? AND capability_id = ? AND receipt_audit_id = ? AND approval_revision = ? AND action_digest = ? AND idempotency_key = ? AND expires_at > ? AND consumed_at IS NULL AND revoked_at IS NULL`), now, command.GrantID, command.InstallationID, command.WorkspaceID, command.TaskID, command.CapabilityID, command.ReceiptAuditID, command.ApprovalRevision, command.ActionDigest, command.IdempotencyKey, now)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return ErrExactTaskCommandUnavailable
	}
	return nil
}

func exactTaskCommandReplay(ctx context.Context, r *Repository, tx *sqlx.Tx, command ExactTaskDescriptionCommand) (ExactTaskDescriptionReceipt, bool, error) {
	var digest, identity string
	var version int64
	err := tx.QueryRowxContext(ctx, r.db.Rebind(`SELECT action_digest, command_identity, resource_version FROM exact_task_command_audits WHERE installation_id = ? AND workspace_id = ? AND idempotency_key = ?`), command.InstallationID, command.WorkspaceID, command.IdempotencyKey).Scan(&digest, &identity, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return ExactTaskDescriptionReceipt{}, false, nil
	}
	if err != nil {
		return ExactTaskDescriptionReceipt{}, true, err
	}
	if digest != command.ActionDigest || identity != exactTaskCommandIdentity(command) {
		return ExactTaskDescriptionReceipt{}, true, ErrExactTaskCommandUnavailable
	}
	return ExactTaskDescriptionReceipt{AuditID: command.IdempotencyKey, ResourceVersion: version}, true, nil
}

func exactTaskCommandIdentity(c ExactTaskDescriptionCommand) string {
	identity := c.GrantID + "\x00" + c.InstallationID + "\x00" + c.WorkspaceID + "\x00" + c.TaskID + "\x00" + c.CapabilityID + "\x00" + c.ReceiptAuditID + "\x00" + fmt.Sprint(c.ApprovalRevision) + "\x00" + c.ActionDigest + "\x00" + c.IdempotencyKey + "\x00" + c.Marker + "\x00" + fmt.Sprint(c.ExpectedResourceVersion) + "\x00" + fmt.Sprint(c.ExpectedFence)
	if c.PendingTransition != nil || c.PendingSnapshotToken != "" {
		identity += "\x00" + c.PendingSnapshotToken + "\x00" + exactPendingTransitionIdentity(c.PendingTransition)
	}
	s := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(s[:])
}

func exactPendingTransitionIdentity(transition *messagequeue.ExactPendingTransition) string {
	if transition == nil {
		return ""
	}
	return transition.SessionID + "\x00" + transition.TaskID + "\x00" + transition.WorkspaceID + "\x00" + transition.SessionIncarnationID + "\x00" + transition.WorkflowID + "\x00" + transition.WorkflowStepID + "\x00" + fmt.Sprint(transition.Position) + "\x00" + transition.QueuedAt.UTC().Format(time.RFC3339Nano) + "\x00" + fmt.Sprint(transition.ResourceVersion) + "\x00" + fmt.Sprint(transition.TaskResourceVersion) + "\x00" + fmt.Sprint(transition.SessionResourceVersion) + "\x00" + fmt.Sprint(transition.QueueGeneration)
}

func requireExactCommandApproval(ctx context.Context, r *Repository, tx *sqlx.Tx, installationID, workspaceID, capabilityID string, revision uint64) error {
	var value int
	err := tx.QueryRowxContext(ctx, r.db.Rebind(`SELECT 1 FROM exact_task_command_approvals WHERE installation_id = ? AND workspace_id = ? AND capability_id = ? AND revision = ? AND revoked_at IS NULL`), installationID, workspaceID, capabilityID, revision).Scan(&value)
	if err != nil {
		return ErrExactTaskCommandUnavailable
	}
	return nil
}
func requireExactCommandReceipt(ctx context.Context, r *Repository, tx *sqlx.Tx, installationID, workspaceID, capabilityID, auditID string, revision uint64) error {
	var value int
	err := tx.QueryRowxContext(ctx, r.db.Rebind(`SELECT 1 FROM exact_task_command_receipts WHERE audit_id = ? AND installation_id = ? AND workspace_id = ? AND capability_id = ? AND approval_revision = ?`), auditID, installationID, workspaceID, capabilityID, revision).Scan(&value)
	if err != nil {
		return ErrExactTaskCommandUnavailable
	}
	return nil
}
func validExactCommandApproval(a ExactTaskCommandApproval) bool {
	return a.InstallationID != "" && a.WorkspaceID != "" && a.CapabilityID != "" && a.ReceiptAuditID != "" && a.Revision > 0
}
func validExactCommandGrant(g ExactTaskCommandGrant) bool {
	return g.ID != "" && g.TaskID != "" && g.ActionDigest != "" && g.IdempotencyKey != "" && validExactCommandApproval(ExactTaskCommandApproval{InstallationID: g.InstallationID, WorkspaceID: g.WorkspaceID, CapabilityID: g.CapabilityID, ReceiptAuditID: g.ReceiptAuditID, Revision: g.ApprovalRevision})
}
func validExactCommand(c ExactTaskDescriptionCommand) bool {
	return c.GrantID != "" && c.TaskID != "" && c.Marker != "" && c.ActionDigest != "" && c.IdempotencyKey != "" && c.ExpectedResourceVersion > 0 && c.ExpectedFence >= 0 && c.CapabilityID == exactTaskDescriptionCommandCapability && validExactCommandApproval(ExactTaskCommandApproval{InstallationID: c.InstallationID, WorkspaceID: c.WorkspaceID, CapabilityID: c.CapabilityID, ReceiptAuditID: c.ReceiptAuditID, Revision: c.ApprovalRevision})
}
