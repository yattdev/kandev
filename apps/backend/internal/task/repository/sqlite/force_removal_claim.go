package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/kandev/kandev/internal/task/models"
)

var (
	ErrForceRemovalClaimStale    = errors.New("force removal claim is stale")
	ErrForceRemovalClaimConflict = errors.New("force removal claim conflicts")
	ErrForceRemovalCleanupHeld   = errors.New("force removal cleanup is held")
)

// ClaimForceRemoval installs one task-scoped admission fence. The claim is
// intentionally private until the service can authorize and quiesce it.
func (r *Repository) ClaimForceRemoval(ctx context.Context, claim *models.ForceRemovalClaim) (*models.ForceRemovalClaim, bool, error) {
	if !validForceRemovalClaim(claim) {
		return nil, false, fmt.Errorf("%w: complete claim identity is required", ErrForceRemovalClaimStale)
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback() }()
	var generation time.Time
	err = tx.QueryRowContext(ctx, r.db.Rebind(`SELECT updated_at FROM tasks WHERE id = ? AND workspace_id = ?`), claim.TaskID, claim.WorkspaceID).Scan(&generation)
	if err != nil || !generation.Equal(claim.TaskGeneration) {
		return nil, false, ErrForceRemovalClaimStale
	}
	var stored models.ForceRemovalClaim
	err = tx.QueryRowContext(ctx, r.db.Rebind(`SELECT task_id, workspace_id, task_generation, admission_generation, operation_id, request_digest, preview_digest, created_at, updated_at FROM task_force_removal_claims WHERE task_id = ?`), claim.TaskID).Scan(&stored.TaskID, &stored.WorkspaceID, &stored.TaskGeneration, &stored.AdmissionGeneration, &stored.OperationID, &stored.RequestDigest, &stored.PreviewDigest, &stored.CreatedAt, &stored.UpdatedAt)
	if err == nil {
		if stored.OperationID == claim.OperationID && stored.RequestDigest == claim.RequestDigest && stored.PreviewDigest == claim.PreviewDigest && stored.AdmissionGeneration == claim.AdmissionGeneration {
			return &stored, true, tx.Commit()
		}
		return nil, false, ErrForceRemovalClaimConflict
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	now := time.Now().UTC()
	claim.CreatedAt, claim.UpdatedAt = now, now
	_, err = tx.ExecContext(ctx, r.db.Rebind(`INSERT INTO task_force_removal_claims (task_id, workspace_id, task_generation, admission_generation, operation_id, request_digest, preview_digest, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`), claim.TaskID, claim.WorkspaceID, claim.TaskGeneration, claim.AdmissionGeneration, claim.OperationID, claim.RequestDigest, claim.PreviewDigest, claim.CreatedAt, claim.UpdatedAt)
	if err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return claim, false, nil
}

func validForceRemovalClaim(claim *models.ForceRemovalClaim) bool {
	return claim != nil && claim.TaskID != "" && claim.WorkspaceID != "" &&
		claim.OperationID != "" && claim.RequestDigest != "" &&
		claim.PreviewDigest != "" && claim.AdmissionGeneration != "" &&
		!claim.TaskGeneration.IsZero()
}

func ensureForceRemovalCleanupAvailableTx(ctx context.Context, db *sqlx.DB, tx *sqlx.Tx, taskID string) error {
	var held bool
	if err := tx.QueryRowContext(ctx, db.Rebind(`SELECT EXISTS (SELECT 1 FROM task_force_removal_claims WHERE task_id = ?)`), taskID).Scan(&held); err != nil {
		return err
	}
	if held {
		return ErrForceRemovalCleanupHeld
	}
	return nil
}
