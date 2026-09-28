package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"

	kandevdb "github.com/kandev/kandev/internal/db"
	"github.com/kandev/kandev/internal/task/models"
)

// CaptureCoordinatorStopOperation commits a durable boundary for one exact
// agentctl incarnation. Queue ownership is deliberately absent: a queue
// incarnation does not identify a replaced agentctl process.
func (r *Repository) CaptureCoordinatorStopOperation(ctx context.Context, operation models.CoordinatorStopOperation) (*models.CoordinatorStopOperation, bool, error) {
	if err := validateCoordinatorStopOperation(operation); err != nil {
		return nil, false, err
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, false, fmt.Errorf("begin coordinator stop operation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := kandevdb.LockTaskRowInTx(ctx, tx, r.db.DriverName(), operation.TaskID); err != nil {
		return nil, false, err
	}
	if err := lockSessionTurnWrites(ctx, tx, r.db.DriverName(), operation.SessionID); err != nil {
		return nil, false, err
	}
	if existing, found, err := getCoordinatorStopOperationTx(ctx, tx, r.db, operation.ID); err != nil || found {
		if err != nil {
			return nil, false, err
		}
		if !sameCoordinatorStopIdentity(existing, operation) {
			return nil, false, errors.New("coordinator stop operation ID is already bound to a different execution")
		}
		return existing, false, tx.Commit()
	}
	if err := matchCoordinatorStopExecutorTx(ctx, tx, r.db, operation); err != nil {
		return nil, false, err
	}
	now := time.Now().UTC()
	if err := closeCoordinatorStopTurnTx(ctx, tx, r.db, operation, now); err != nil {
		return nil, false, err
	}
	if err := cancelCoordinatorStopSessionTx(ctx, tx, r.db, operation, now); err != nil {
		return nil, false, err
	}
	// Final CAS proves the recorded status/version remained current through the
	// durable writes. Failure rolls back the turn and session settlement.
	if err := matchCoordinatorStopExecutorTx(ctx, tx, r.db, operation); err != nil {
		return nil, false, err
	}
	operation.AdmissionCutoff, operation.Status, operation.ProofScope = now, models.CoordinatorStopOperationStatusFencing, models.CoordinatorStopProofScopePending
	operation.CreatedAt, operation.UpdatedAt = now, now
	_, err = tx.ExecContext(ctx, r.db.Rebind(`INSERT INTO task_stop_operations (id, task_id, session_id, turn_id, execution_id, agentctl_generation, executor_status, executor_updated_at, admission_cutoff, status, reason_code, proof_scope, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`), operation.ID, operation.TaskID, operation.SessionID, operation.TurnID, operation.ExecutionID, operation.AgentctlGeneration, operation.ExecutorStatus, operation.ExecutorUpdatedAt, operation.AdmissionCutoff, operation.Status, operation.ReasonCode, operation.ProofScope, operation.CreatedAt, operation.UpdatedAt)
	if err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return &operation, true, nil
}

func validateCoordinatorStopOperation(operation models.CoordinatorStopOperation) error {
	if operation.ID == "" || operation.TaskID == "" || operation.SessionID == "" || operation.TurnID == "" || operation.ExecutionID == "" || operation.AgentctlGeneration == 0 || operation.ExecutorStatus == "" || operation.ExecutorUpdatedAt.IsZero() {
		return errors.New("coordinator stop operation requires exact operation, task, session, turn, execution, agentctl generation, executor status, and executor version")
	}
	return nil
}

func sameCoordinatorStopIdentity(existing *models.CoordinatorStopOperation, operation models.CoordinatorStopOperation) bool {
	return existing.TaskID == operation.TaskID && existing.SessionID == operation.SessionID &&
		existing.TurnID == operation.TurnID && existing.ExecutionID == operation.ExecutionID &&
		existing.AgentctlGeneration == operation.AgentctlGeneration
}

func matchCoordinatorStopExecutorTx(ctx context.Context, tx *sqlx.Tx, db *sqlx.DB, operation models.CoordinatorStopOperation) error {
	result, err := tx.ExecContext(ctx, db.Rebind(`UPDATE executors_running SET updated_at = updated_at WHERE session_id = ? AND task_id = ? AND agent_execution_id = ? AND agentctl_generation = ? AND status = ? AND updated_at = ?`), operation.SessionID, operation.TaskID, operation.ExecutionID, operation.AgentctlGeneration, operation.ExecutorStatus, operation.ExecutorUpdatedAt)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return models.ErrExecutionRotated
	}
	return nil
}

func closeCoordinatorStopTurnTx(ctx context.Context, tx *sqlx.Tx, db *sqlx.DB, operation models.CoordinatorStopOperation, now time.Time) error {
	result, err := tx.ExecContext(ctx, db.Rebind(`UPDATE task_session_turns SET completed_at = ?, updated_at = ? WHERE id = ? AND task_session_id = ? AND task_id = ? AND completed_at IS NULL`), now, now, operation.TurnID, operation.SessionID, operation.TaskID)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return models.ErrExecutionRotated
	}
	return nil
}

func cancelCoordinatorStopSessionTx(ctx context.Context, tx *sqlx.Tx, db *sqlx.DB, operation models.CoordinatorStopOperation, now time.Time) error {
	result, err := tx.ExecContext(ctx, db.Rebind(`UPDATE task_sessions SET state = ?, completed_at = ?, updated_at = ? WHERE id = ? AND task_id = ? AND state IN ('CREATED', 'STARTING', 'RUNNING', 'WAITING_FOR_INPUT', 'IDLE')`), string(models.TaskSessionStateCancelled), now, now, operation.SessionID, operation.TaskID)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return models.ErrExecutionRotated
	}
	return nil
}

func (r *Repository) GetCoordinatorStopOperation(ctx context.Context, operationID string) (*models.CoordinatorStopOperation, error) {
	if operationID == "" {
		return nil, errors.New("coordinator stop operation ID is required")
	}
	operation := &models.CoordinatorStopOperation{}
	err := r.ro.QueryRowxContext(ctx, r.ro.Rebind(`SELECT id, task_id, session_id, turn_id, execution_id, agentctl_generation, executor_status, executor_updated_at, admission_cutoff, status, reason_code, proof_scope, created_at, updated_at FROM task_stop_operations WHERE id = ?`), operationID).Scan(&operation.ID, &operation.TaskID, &operation.SessionID, &operation.TurnID, &operation.ExecutionID, &operation.AgentctlGeneration, &operation.ExecutorStatus, &operation.ExecutorUpdatedAt, &operation.AdmissionCutoff, &operation.Status, &operation.ReasonCode, &operation.ProofScope, &operation.CreatedAt, &operation.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("coordinator stop operation not found: %s", operationID)
	}
	if err != nil {
		return nil, err
	}
	return operation, nil
}

// MarkCoordinatorStopOperationIncomplete records that exact runtime proof was
// unavailable. It cannot manufacture a stopped receipt, and a replacement
// agentctl generation cannot mutate the predecessor's operation.
func (r *Repository) MarkCoordinatorStopOperationIncomplete(
	ctx context.Context, operationID, executionID string, agentctlGeneration uint64, reasonCode string,
) (*models.CoordinatorStopOperation, bool, error) {
	if operationID == "" || executionID == "" || agentctlGeneration == 0 || reasonCode == "" {
		return nil, false, errors.New("incomplete stop operation requires operation, execution, agentctl generation, and reason")
	}
	now := time.Now().UTC()
	result, err := r.db.ExecContext(ctx, r.db.Rebind(`
		UPDATE task_stop_operations
		SET status = ?, reason_code = ?, updated_at = ?
		WHERE id = ? AND execution_id = ? AND agentctl_generation = ? AND status = ?
	`), models.CoordinatorStopOperationStatusIncomplete, reasonCode, now, operationID, executionID, agentctlGeneration, models.CoordinatorStopOperationStatusFencing)
	if err != nil {
		return nil, false, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return nil, false, err
	}
	operation, err := r.GetCoordinatorStopOperation(ctx, operationID)
	if err != nil {
		return nil, false, err
	}
	if operation.ExecutionID != executionID || operation.AgentctlGeneration != agentctlGeneration {
		return nil, false, models.ErrExecutionRotated
	}
	if changed == 0 && operation.Status != models.CoordinatorStopOperationStatusIncomplete {
		return nil, false, models.ErrExecutionRotated
	}
	return operation, changed == 1, nil
}

// ConsumeCoordinatorStopFenceReceipt records agentctl's exact admission fence
// only after the captured executor row and turn remain current. It never writes
// stopped: even a drained agentctl receipt lacks the lifecycle-owned terminal
// executor proof.
func (r *Repository) ConsumeCoordinatorStopFenceReceipt(
	ctx context.Context, operationID string, receipt models.CoordinatorStopFenceReceipt,
) (*models.CoordinatorStopOperation, error) {
	if operationID == "" || receipt.ExecutionID == "" || receipt.AgentctlGeneration == 0 || receipt.AdmissionClosedAt.IsZero() {
		return nil, errors.New("coordinator stop fence receipt requires operation, execution, agentctl generation, and cutoff")
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin coordinator stop receipt: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	op, err := loadCoordinatorStopReceiptOperationTx(ctx, tx, r.db, r.db.DriverName(), operationID, receipt)
	if err != nil {
		return nil, err
	}
	if err := validateCoordinatorStopReceiptBoundaryTx(ctx, tx, r.db, op); err != nil {
		return nil, err
	}
	if err := markCoordinatorStopReceiptIncompleteTx(ctx, tx, r.db, operationID, receipt); err != nil {
		return nil, err
	}
	updated, found, err := getCoordinatorStopOperationTx(ctx, tx, r.db, operationID)
	if err != nil || !found {
		if err != nil {
			return nil, err
		}
		return nil, models.ErrExecutionRotated
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return updated, nil
}

func loadCoordinatorStopReceiptOperationTx(ctx context.Context, tx *sqlx.Tx, db *sqlx.DB, driverName, operationID string, receipt models.CoordinatorStopFenceReceipt) (*models.CoordinatorStopOperation, error) {
	op, found, err := getCoordinatorStopOperationTx(ctx, tx, db, operationID)
	if err != nil {
		return nil, err
	}
	if !found || op.ExecutionID != receipt.ExecutionID || op.AgentctlGeneration != receipt.AgentctlGeneration {
		return nil, models.ErrExecutionRotated
	}
	if err := kandevdb.LockTaskRowInTx(ctx, tx, driverName, op.TaskID); err != nil {
		return nil, err
	}
	if err := lockSessionTurnWrites(ctx, tx, driverName, op.SessionID); err != nil {
		return nil, err
	}
	return op, nil
}

func validateCoordinatorStopReceiptBoundaryTx(ctx context.Context, tx *sqlx.Tx, db *sqlx.DB, op *models.CoordinatorStopOperation) error {
	if err := matchCoordinatorStopExecutorTx(ctx, tx, db, *op); err != nil {
		return err
	}
	var completed, successorOpen bool
	if err := tx.GetContext(ctx, &completed, db.Rebind(`SELECT EXISTS(SELECT 1 FROM task_session_turns WHERE id = ? AND task_session_id = ? AND task_id = ? AND completed_at IS NOT NULL)`), op.TurnID, op.SessionID, op.TaskID); err != nil {
		return err
	}
	if !completed {
		return models.ErrExecutionRotated
	}
	if err := tx.GetContext(ctx, &successorOpen, db.Rebind(`SELECT EXISTS(SELECT 1 FROM task_session_turns WHERE task_session_id = ? AND task_id = ? AND id <> ? AND completed_at IS NULL)`), op.SessionID, op.TaskID, op.TurnID); err != nil {
		return err
	}
	if successorOpen {
		return models.ErrExecutionRotated
	}
	return nil
}

func markCoordinatorStopReceiptIncompleteTx(ctx context.Context, tx *sqlx.Tx, db *sqlx.DB, operationID string, receipt models.CoordinatorStopFenceReceipt) error {
	reasonCode := "executor_terminal_proof_pending"
	if !receipt.ManagedProcessesDrained {
		reasonCode = "managed_processes_not_drained"
	}
	result, err := tx.ExecContext(ctx, db.Rebind(`UPDATE task_stop_operations SET status = ?, reason_code = ?, proof_scope = ?, updated_at = ? WHERE id = ? AND execution_id = ? AND agentctl_generation = ? AND status IN (?, ?)`), models.CoordinatorStopOperationStatusIncomplete, reasonCode, models.CoordinatorStopProofScopeAgentctlFence, time.Now().UTC(), operationID, receipt.ExecutionID, receipt.AgentctlGeneration, models.CoordinatorStopOperationStatusFencing, models.CoordinatorStopOperationStatusIncomplete)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return models.ErrExecutionRotated
	}
	return nil
}

func getCoordinatorStopOperationTx(ctx context.Context, tx *sqlx.Tx, db *sqlx.DB, id string) (*models.CoordinatorStopOperation, bool, error) {
	operation := &models.CoordinatorStopOperation{}
	err := tx.QueryRowxContext(ctx, db.Rebind(`SELECT id, task_id, session_id, turn_id, execution_id, agentctl_generation, executor_status, executor_updated_at, admission_cutoff, status, reason_code, proof_scope, created_at, updated_at FROM task_stop_operations WHERE id = ?`), id).Scan(&operation.ID, &operation.TaskID, &operation.SessionID, &operation.TurnID, &operation.ExecutionID, &operation.AgentctlGeneration, &operation.ExecutorStatus, &operation.ExecutorUpdatedAt, &operation.AdmissionCutoff, &operation.Status, &operation.ReasonCode, &operation.ProofScope, &operation.CreatedAt, &operation.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return operation, true, nil
}
