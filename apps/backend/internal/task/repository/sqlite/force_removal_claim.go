package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/kandev/kandev/internal/db/dialect"
	"github.com/kandev/kandev/internal/task/models"
)

var (
	ErrForceRemovalClaimStale    = errors.New("force removal claim is stale")
	ErrForceRemovalClaimConflict = errors.New("force removal claim conflicts")
	ErrForceRemovalTaskHeld      = models.ErrForceRemovalTaskHeld
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
	query := `SELECT updated_at FROM tasks WHERE id = ? AND workspace_id = ?`
	if dialect.IsPostgres(r.db.DriverName()) {
		query += forUpdateClause
	}
	var generation time.Time
	err = tx.QueryRowContext(ctx, r.db.Rebind(query), claim.TaskID, claim.WorkspaceID).Scan(&generation)
	if err != nil || !generation.Equal(claim.TaskGeneration) {
		return nil, false, ErrForceRemovalClaimStale
	}
	now := time.Now().UTC()
	claim.CreatedAt, claim.UpdatedAt = now, now
	result, err := tx.ExecContext(ctx, r.db.Rebind(`
		INSERT INTO task_force_removal_claims (
			task_id, workspace_id, task_generation, admission_generation,
			operation_id, request_digest, preview_digest, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(task_id) DO NOTHING
	`), claim.TaskID, claim.WorkspaceID, claim.TaskGeneration, claim.AdmissionGeneration, claim.OperationID, claim.RequestDigest, claim.PreviewDigest, claim.CreatedAt, claim.UpdatedAt)
	if err != nil {
		var operationTaskID string
		lookupErr := tx.QueryRowContext(ctx, r.db.Rebind(`SELECT task_id FROM task_force_removal_claims WHERE operation_id = ?`), claim.OperationID).Scan(&operationTaskID)
		if lookupErr == nil {
			return nil, false, ErrForceRemovalClaimConflict
		}
		return nil, false, err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return nil, false, err
	}
	if inserted == 0 {
		stored, err := loadForceRemovalClaimTx(ctx, r.db, tx, claim.TaskID)
		if err != nil {
			return nil, false, err
		}
		if sameForceRemovalClaim(stored, claim) {
			return stored, true, tx.Commit()
		}
		return nil, false, ErrForceRemovalClaimConflict
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return claim, false, nil
}

func loadForceRemovalClaimTx(ctx context.Context, db *sqlx.DB, tx *sqlx.Tx, taskID string) (*models.ForceRemovalClaim, error) {
	var claim models.ForceRemovalClaim
	err := tx.QueryRowContext(ctx, db.Rebind(`
		SELECT task_id, workspace_id, task_generation, admission_generation,
			operation_id, request_digest, preview_digest, created_at, updated_at
		FROM task_force_removal_claims WHERE task_id = ?
	`), taskID).Scan(
		&claim.TaskID, &claim.WorkspaceID, &claim.TaskGeneration,
		&claim.AdmissionGeneration, &claim.OperationID, &claim.RequestDigest,
		&claim.PreviewDigest, &claim.CreatedAt, &claim.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrForceRemovalClaimStale
	}
	if err != nil {
		return nil, err
	}
	return &claim, nil
}

func sameForceRemovalClaim(stored, claim *models.ForceRemovalClaim) bool {
	return stored.OperationID == claim.OperationID &&
		stored.RequestDigest == claim.RequestDigest &&
		stored.PreviewDigest == claim.PreviewDigest &&
		stored.AdmissionGeneration == claim.AdmissionGeneration
}

func validForceRemovalClaim(claim *models.ForceRemovalClaim) bool {
	return claim != nil && claim.TaskID != "" && claim.WorkspaceID != "" &&
		claim.OperationID != "" && claim.RequestDigest != "" &&
		claim.PreviewDigest != "" && claim.AdmissionGeneration != "" &&
		!claim.TaskGeneration.IsZero()
}

// AppendForceRemovalReceipt appends immutable redacted evidence to one claim.
func (r *Repository) AppendForceRemovalReceipt(ctx context.Context, operationID string, receipt models.ExactRetirementPredicateReceipt) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockForceRemovalClaimTx(ctx, r.db, tx, operationID); err != nil {
		return err
	}
	var ordinal int
	if err := tx.QueryRowContext(ctx, r.db.Rebind(`SELECT COALESCE(MAX(ordinal) + 1, 0) FROM task_force_removal_receipts WHERE operation_id = ?`), operationID).Scan(&ordinal); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, r.db.Rebind(`
		INSERT INTO task_force_removal_receipts (
			operation_id, ordinal, predicate, status, reason_code, resource_id,
			observed_generation, evidence_digest, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(operation_id, predicate) DO NOTHING
	`), operationID, ordinal, receipt.Predicate, receipt.Status, receipt.ReasonCode, receipt.ResourceID, receipt.ObservedGeneration, receipt.EvidenceDigest, time.Now().UTC())
	if err != nil {
		return err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if inserted == 0 {
		stored, err := loadForceRemovalReceiptTx(ctx, r.db, tx, operationID, receipt.Predicate)
		if err != nil {
			return err
		}
		if !sameForceRemovalReceipt(stored, receipt) {
			return ErrForceRemovalClaimConflict
		}
	}
	return tx.Commit()
}

func lockForceRemovalClaimTx(ctx context.Context, db *sqlx.DB, tx *sqlx.Tx, operationID string) error {
	result, err := tx.ExecContext(ctx, db.Rebind(`
		UPDATE task_force_removal_claims SET updated_at = updated_at
		WHERE operation_id = ?
	`), operationID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return ErrForceRemovalClaimStale
	}
	return nil
}

func loadForceRemovalReceiptTx(ctx context.Context, db *sqlx.DB, tx *sqlx.Tx, operationID string, predicate models.ExactRetirementPredicate) (models.ExactRetirementPredicateReceipt, error) {
	var receipt models.ExactRetirementPredicateReceipt
	err := tx.QueryRowContext(ctx, db.Rebind(`
		SELECT predicate, status, reason_code, resource_id, observed_generation, evidence_digest
		FROM task_force_removal_receipts WHERE operation_id = ? AND predicate = ?
	`), operationID, predicate).Scan(
		&receipt.Predicate, &receipt.Status, &receipt.ReasonCode, &receipt.ResourceID,
		&receipt.ObservedGeneration, &receipt.EvidenceDigest,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return models.ExactRetirementPredicateReceipt{}, ErrForceRemovalClaimStale
	}
	return receipt, err
}

func sameForceRemovalReceipt(stored, incoming models.ExactRetirementPredicateReceipt) bool {
	return stored.Predicate == incoming.Predicate &&
		stored.Status == incoming.Status &&
		stored.ReasonCode == incoming.ReasonCode &&
		stored.ResourceID == incoming.ResourceID &&
		stored.ObservedGeneration == incoming.ObservedGeneration &&
		stored.EvidenceDigest == incoming.EvidenceDigest
}

func (r *Repository) ListForceRemovalReceipts(ctx context.Context, operationID string) ([]models.ExactRetirementPredicateReceipt, error) {
	rows, err := r.ro.QueryContext(ctx, r.ro.Rebind(`SELECT predicate, status, reason_code, resource_id, observed_generation, evidence_digest FROM task_force_removal_receipts WHERE operation_id = ? ORDER BY ordinal`), operationID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var receipts []models.ExactRetirementPredicateReceipt
	for rows.Next() {
		var receipt models.ExactRetirementPredicateReceipt
		if err := rows.Scan(&receipt.Predicate, &receipt.Status, &receipt.ReasonCode, &receipt.ResourceID, &receipt.ObservedGeneration, &receipt.EvidenceDigest); err != nil {
			return nil, err
		}
		receipts = append(receipts, receipt)
	}
	return receipts, rows.Err()
}

func ensureForceRemovalCleanupAvailableTx(ctx context.Context, db *sqlx.DB, tx *sqlx.Tx, taskID string) error {
	if err := ensureForceRemovalTaskAvailableTx(ctx, db, tx, taskID); err != nil {
		if errors.Is(err, ErrForceRemovalTaskHeld) {
			return ErrForceRemovalCleanupHeld
		}
		return err
	}
	return nil
}

func ensureForceRemovalTaskAvailableTx(ctx context.Context, db *sqlx.DB, tx *sqlx.Tx, taskID string) error {
	var held bool
	if err := tx.QueryRowContext(ctx, db.Rebind(`SELECT EXISTS (SELECT 1 FROM task_force_removal_claims WHERE task_id = ?)`), taskID).Scan(&held); err != nil {
		return err
	}
	if held {
		return ErrForceRemovalTaskHeld
	}
	return nil
}

func ensureForceRemovalTaskAvailableStdTx(ctx context.Context, db *sqlx.DB, tx *sql.Tx, taskID string) error {
	var held bool
	if err := tx.QueryRowContext(ctx, db.Rebind(`SELECT EXISTS (SELECT 1 FROM task_force_removal_claims WHERE task_id = ?)`), taskID).Scan(&held); err != nil {
		return err
	}
	if held {
		return ErrForceRemovalTaskHeld
	}
	return nil
}

func ensureForceRemovalMessageAvailableTx(ctx context.Context, db *sqlx.DB, tx *sqlx.Tx, sessionID string) error {
	var taskID string
	err := tx.QueryRowContext(ctx, db.Rebind(`SELECT task_id FROM task_sessions WHERE id = ?`), sessionID).Scan(&taskID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return ensureForceRemovalTaskAvailableTx(ctx, db, tx, taskID)
}
