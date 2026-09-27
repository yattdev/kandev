package provideraccess

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
)

// ExposureReceipt records only the provider authority that has left the Host.
// It never contains token bytes or information sufficient to recreate a token.
// Expected is Host-verified input; a plugin-supplied identity is not authority.
type ExposureReceipt struct {
	LeaseID             string
	GrantID             string
	Provider            string
	ProviderPrincipalID string
	RepositoryID        string
	PermissionProfile   string
	ProviderExpiresAt   time.Time
	Expected            FinalLeaseIdentity
}

// FinalLeaseIdentity is the exact non-secret identity checked under the grant
// and lease row locks before a future caller can export a provider bearer.
type FinalLeaseIdentity struct {
	GrantGeneration      int64
	Scope                GrantScope
	ManagedTaskID        string
	SessionID            string
	TargetDigest         string
	ApprovalRevision     uint64
	ConnectionGeneration string
}

// ExposureState separates a fenced lease from a bearer revoked by the provider.
type ExposureState string

var ErrRevocationUnconfirmed = errors.New("provider access token revocation unconfirmed")

const (
	ExposureUnknown           ExposureState = "unknown"
	ExposureActive            ExposureState = "exported"
	ExposureResidual          ExposureState = "expiry_bounded_residual"
	ExposureRevokedAtProvider ExposureState = "revoked_at_provider"
	ExposureProviderExpired   ExposureState = "provider_expired"
)

// RecordExposureOrRevoke admits the exposure before a future caller exports a
// freshly minted token. A losing admission race revokes that exact token.
// The revoker owns token bytes; this store never receives them.
func (s *Store) RecordExposureOrRevoke(
	ctx context.Context, receipt ExposureReceipt, revoke func(context.Context) error,
) error {
	return s.recordExposureOrRevoke(ctx, receipt, nil, revoke)
}

// RecordExposureWithAuditOrRevoke commits the non-secret request correlation
// in the same transaction as final exposure admission, before token export.
func (s *Store) RecordExposureWithAuditOrRevoke(
	ctx context.Context, receipt ExposureReceipt, audit AuditEvent, revoke func(context.Context) error,
) error {
	return s.recordExposureOrRevoke(ctx, receipt, &audit, revoke)
}

func (s *Store) recordExposureOrRevoke(
	ctx context.Context, receipt ExposureReceipt, audit *AuditEvent, revoke func(context.Context) error,
) error {
	if revoke == nil {
		return errors.New("provider access exact-token revoker is required")
	}
	err := s.recordExposureReceipt(ctx, receipt, audit)
	if err == nil {
		return nil
	}
	revokeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if revokeErr := revoke(revokeCtx); revokeErr != nil {
		// Provider error bodies can contain secrets; preserve only the outcome.
		return errors.Join(err, ErrRevocationUnconfirmed)
	}
	return err
}

// recordExposureReceipt serializes final admission with grant/lease revocation.
func (s *Store) recordExposureReceipt(ctx context.Context, receipt ExposureReceipt, audit *AuditEvent) error {
	if err := validateExposureAdmission(receipt, audit); err != nil {
		return err
	}
	key, err := scopeKey(receipt.Expected.Scope)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// The no-op writes acquire the same grant-then-lease row locks as revocation.
	result, err := tx.ExecContext(ctx, tx.Rebind(`UPDATE provider_access_grants
  SET updated_at = updated_at WHERE id = ? AND scope_key = ? AND generation = ?
  AND revoked_at IS NULL AND expires_at > ?`),
		receipt.GrantID, key, receipt.Expected.GrantGeneration, now.Unix())
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrGrantUnavailable
	}
	result, err = tx.ExecContext(ctx, tx.Rebind(`UPDATE provider_access_leases
  SET created_at = created_at WHERE id = ? AND grant_id = ?
  AND grant_generation = ? AND scope_key = ? AND managed_task_id = ?
  AND session_id = ? AND target_digest = ? AND approval_revision = ?
  AND connection_generation = ? AND revoked_at IS NULL AND expires_at > ?`),
		receipt.LeaseID, receipt.GrantID, receipt.Expected.GrantGeneration, key,
		receipt.Expected.ManagedTaskID, receipt.Expected.SessionID,
		receipt.Expected.TargetDigest, int64(receipt.Expected.ApprovalRevision),
		receipt.Expected.ConnectionGeneration, now.Unix())
	if err != nil {
		return err
	}
	count, err = result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrGrantUnavailable
	}
	result, err = tx.ExecContext(ctx, tx.Rebind(`INSERT INTO provider_access_exposures (
  lease_id, grant_id, provider, provider_principal_id, repository_id,
  permission_profile, provider_expires_at, exported_at)
  SELECT l.id, l.grant_id, g.provider, ?, g.repository_id, ?, ?, ?
  FROM provider_access_leases l JOIN provider_access_grants g ON g.id = l.grant_id
  WHERE l.id = ? AND l.grant_id = ? AND g.provider = ? AND g.repository_id = ?
  AND l.revoked_at IS NULL AND l.expires_at > ? AND g.revoked_at IS NULL
  AND g.expires_at > ? AND g.generation = l.grant_generation
  AND EXISTS (SELECT 1 FROM provider_access_redemptions r
    WHERE r.lease_id = l.id AND r.grant_id = g.id)`),
		receipt.ProviderPrincipalID, receipt.PermissionProfile,
		receipt.ProviderExpiresAt.Unix(), now.Unix(), receipt.LeaseID,
		receipt.GrantID, receipt.Provider, receipt.RepositoryID, now.Unix(), now.Unix())
	if err != nil {
		return err
	}
	count, err = result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrGrantUnavailable
	}
	if err := insertExposureAudit(ctx, tx, audit); err != nil {
		return err
	}
	return tx.Commit()
}

func validateExposureAdmission(receipt ExposureReceipt, audit *AuditEvent) error {
	if err := validateExposureReceipt(receipt); err != nil {
		return err
	}
	if audit != nil {
		return validateExposureAudit(receipt, *audit)
	}
	return nil
}

func insertExposureAudit(ctx context.Context, tx *sqlx.Tx, audit *AuditEvent) error {
	if audit == nil {
		return nil
	}
	return insertAudit(ctx, tx, *audit)
}

func validateExposureAudit(receipt ExposureReceipt, audit AuditEvent) error {
	if err := validateAudit(audit); err != nil {
		return err
	}
	if audit.Outcome != AuditTokenIssued || audit.RequestIDHash == "" {
		return ErrGrantUnavailable
	}
	identity := auditIdentity{audit.LeaseID, audit.GrantID, audit.PluginInstallationID,
		audit.WorkspaceID, audit.ManagedTaskID, audit.SessionID, audit.TargetDigest,
		audit.GrantGeneration, audit.ApprovalRevision, audit.ConnectionGeneration,
		audit.Provider, audit.Purpose}
	expected := auditIdentity{receipt.LeaseID, receipt.GrantID,
		receipt.Expected.Scope.PluginInstallationID, receipt.Expected.Scope.WorkspaceID,
		receipt.Expected.ManagedTaskID, receipt.Expected.SessionID, receipt.Expected.TargetDigest,
		receipt.Expected.GrantGeneration, receipt.Expected.ApprovalRevision,
		receipt.Expected.ConnectionGeneration, receipt.Provider, receipt.Expected.Scope.Purpose}
	if identity != expected {
		return ErrGrantUnavailable
	}
	return nil
}

type auditIdentity struct {
	leaseID, grantID, installationID, workspaceID, taskID, sessionID, targetDigest string
	grantGeneration                                                                int64
	approvalRevision                                                               uint64
	connectionGeneration, provider, purpose                                        string
}

func validateExposureReceipt(receipt ExposureReceipt) error {
	for _, value := range []string{receipt.LeaseID, receipt.GrantID, receipt.ProviderPrincipalID,
		receipt.RepositoryID, receipt.Expected.ManagedTaskID, receipt.Expected.SessionID,
		receipt.Expected.TargetDigest, receipt.Expected.ConnectionGeneration} {
		if value == "" || value != strings.TrimSpace(value) {
			return errors.New("complete provider exposure identity is required")
		}
	}
	if receipt.Provider != "github" || receipt.PermissionProfile != "github_actions_rerun" {
		return errors.New("unsupported provider exposure profile")
	}
	if receipt.Expected.GrantGeneration <= 0 || receipt.Expected.ApprovalRevision == 0 ||
		receipt.Expected.ApprovalRevision > math.MaxInt64 ||
		receipt.Provider != receipt.Expected.Scope.Provider ||
		receipt.RepositoryID != receipt.Expected.Scope.RepositoryID {
		return ErrGrantUnavailable
	}
	now := time.Now().UTC()
	if !receipt.ProviderExpiresAt.After(now) || receipt.ProviderExpiresAt.After(now.Add(time.Hour+time.Minute)) {
		return errors.New("invalid provider token expiry")
	}
	return nil
}

// RecordRevocationResult records the attempted provider result, not a lease policy decision.
// confirmed must be true only after a successful provider revocation response.
func (s *Store) RecordRevocationResult(ctx context.Context, leaseID string, at time.Time, confirmed bool) error {
	if leaseID == "" || at.IsZero() {
		return errors.New("lease and revocation attempt time are required")
	}
	var confirmedAt any
	if confirmed {
		confirmedAt = at.Unix()
	}
	result, err := s.db.ExecContext(ctx, s.db.Rebind(`UPDATE provider_access_exposures
  SET revocation_attempted_at = ?, revoked_at_provider = ?
  WHERE lease_id = ? AND revoked_at_provider IS NULL`), at.Unix(), confirmedAt, leaseID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return sql.ErrNoRows
	}
	return nil
}

// ExposureStateAt reports provider invalidation or the remaining bearer window.
func (s *Store) ExposureStateAt(ctx context.Context, leaseID string, at time.Time) (ExposureState, error) {
	var row struct {
		ProviderExpiresAt     int64         `db:"provider_expires_at"`
		LeaseExpiresAt        int64         `db:"lease_expires_at"`
		RevocationAttemptedAt sql.NullInt64 `db:"revocation_attempted_at"`
		RevokedAtProvider     sql.NullInt64 `db:"revoked_at_provider"`
		LeaseRevokedAt        sql.NullInt64 `db:"lease_revoked_at"`
		GrantRevokedAt        sql.NullInt64 `db:"grant_revoked_at"`
	}
	err := s.db.GetContext(ctx, &row, s.db.Rebind(`SELECT e.provider_expires_at,
  l.expires_at AS lease_expires_at, e.revocation_attempted_at,
  e.revoked_at_provider, l.revoked_at AS lease_revoked_at,
  g.revoked_at AS grant_revoked_at
  FROM provider_access_exposures e
  JOIN provider_access_leases l ON l.id = e.lease_id
  JOIN provider_access_grants g ON g.id = e.grant_id
  WHERE e.lease_id = ?`), leaseID)
	if errors.Is(err, sql.ErrNoRows) {
		return ExposureUnknown, nil
	}
	if err != nil {
		return ExposureUnknown, err
	}
	if row.RevokedAtProvider.Valid {
		return ExposureRevokedAtProvider, nil
	}
	if at.Unix() >= row.ProviderExpiresAt {
		return ExposureProviderExpired, nil
	}
	if at.Unix() >= row.LeaseExpiresAt || row.RevocationAttemptedAt.Valid ||
		row.LeaseRevokedAt.Valid || row.GrantRevokedAt.Valid {
		return ExposureResidual, nil
	}
	return ExposureActive, nil
}

// HasUnexpiredWorkspaceAuthority reports bearer exposure and ambiguous mint
// attempts that remain possible after a Host restart. Neither lease expiry nor
// grant revocation invalidates an already exported provider token.
func (s *Store) HasUnexpiredWorkspaceAuthority(ctx context.Context, workspaceID string, at time.Time) (bool, error) {
	if workspaceID == "" || at.IsZero() {
		return false, ErrGrantUnavailable
	}
	var exposed int
	err := s.db.GetContext(ctx, &exposed, s.db.Rebind(`SELECT COUNT(*)
  FROM provider_access_exposures e JOIN provider_access_grants g ON g.id = e.grant_id
  WHERE g.workspace_id = ? AND e.provider_expires_at > ? AND e.revoked_at_provider IS NULL`),
		workspaceID, at.Unix())
	if err != nil || exposed > 0 {
		return exposed > 0, err
	}
	var unknown int
	err = s.db.GetContext(ctx, &unknown, s.db.Rebind(`SELECT COUNT(*)
  FROM provider_access_redemptions r JOIN provider_access_grants g ON g.id = r.grant_id
  LEFT JOIN provider_access_exposures e ON e.lease_id = r.lease_id
  WHERE g.workspace_id = ? AND r.possible_provider_expiry > ? AND e.lease_id IS NULL`),
		workspaceID, at.Unix())
	return unknown > 0, err
}
