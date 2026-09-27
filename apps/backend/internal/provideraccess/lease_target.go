package provideraccess

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/jmoiron/sqlx"
)

// GetLeaseTarget reloads the immutable provider selector needed for a fresh
// provider read after Host restart. Authority must separately inspect the live
// lease and grant before using it.
func (s *Store) GetLeaseTarget(ctx context.Context, leaseID string) (*GitHubRerunTarget, error) {
	var row struct {
		TargetJSON   string `db:"target_json"`
		TargetDigest string `db:"target_digest"`
	}
	err := s.db.GetContext(ctx, &row, s.db.Rebind(`SELECT t.target_json, l.target_digest
  FROM provider_access_targets t JOIN provider_access_leases l ON l.id = t.lease_id
  WHERE t.lease_id = ?`), leaseID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var target GitHubRerunTarget
	if err := json.Unmarshal([]byte(row.TargetJSON), &target); err != nil {
		return nil, ErrTargetInvalid
	}
	digest, err := target.Digest()
	if err != nil || digest != row.TargetDigest {
		return nil, ErrTargetInvalid
	}
	return &target, nil
}

func leaseTargetMatches(ctx context.Context, tx *sqlx.Tx, leaseID string, target GitHubRerunTarget) (bool, error) {
	var encoded string
	err := tx.GetContext(ctx, &encoded, tx.Rebind(`SELECT target_json
  FROM provider_access_targets WHERE lease_id = ?`), leaseID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var stored GitHubRerunTarget
	if err := json.Unmarshal([]byte(encoded), &stored); err != nil {
		return false, ErrTargetInvalid
	}
	return stored == target, nil
}

func insertLeaseTarget(ctx context.Context, tx *sqlx.Tx, claim LeaseClaim) error {
	if claim.Target == nil {
		return nil
	}
	encoded, err := json.Marshal(claim.Target)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, tx.Rebind(`INSERT INTO provider_access_targets
  (lease_id, target_json) VALUES (?, ?)`), claim.ID, string(encoded))
	return err
}

func replayExistingLease(
	ctx context.Context, tx *sqlx.Tx, existing leaseRow, claim LeaseClaim, key string, now time.Time,
) (*Lease, error) {
	if !existing.matches(claim, key) || existing.RevokedAt.Valid || existing.ExpiresAt <= now.Unix() {
		return nil, ErrLeaseConflict
	}
	if claim.Target != nil {
		matches, err := leaseTargetMatches(ctx, tx, existing.ID, *claim.Target)
		if err != nil {
			return nil, err
		}
		if !matches {
			return nil, ErrLeaseConflict
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	lease := existing.lease(claim.Scope)
	lease.Replayed = true
	return lease, nil
}
