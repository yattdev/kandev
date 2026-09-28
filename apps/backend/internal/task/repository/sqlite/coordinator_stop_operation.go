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

func (r *Repository) ListPendingCoordinatorStopOperations(ctx context.Context, taskID string) ([]*models.CoordinatorStopOperation, error) {
	rows, err := r.ro.QueryxContext(ctx, r.ro.Rebind(`
		SELECT id, task_id, session_id, turn_id, execution_id, agentctl_generation, executor_status, executor_updated_at, admission_cutoff, status, reason_code, proof_scope, created_at, updated_at
		FROM task_stop_operations
		WHERE task_id = ? AND status IN (?, ?)
		ORDER BY created_at ASC, id ASC
	`), taskID, models.CoordinatorStopOperationStatusFencing, models.CoordinatorStopOperationStatusIncomplete)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var operations []*models.CoordinatorStopOperation
	for rows.Next() {
		operation := &models.CoordinatorStopOperation{}
		if err := rows.Scan(&operation.ID, &operation.TaskID, &operation.SessionID, &operation.TurnID, &operation.ExecutionID, &operation.AgentctlGeneration, &operation.ExecutorStatus, &operation.ExecutorUpdatedAt, &operation.AdmissionCutoff, &operation.Status, &operation.ReasonCode, &operation.ProofScope, &operation.CreatedAt, &operation.UpdatedAt); err != nil {
			return nil, err
		}
		operations = append(operations, operation)
	}
	return operations, rows.Err()
}

// ListCoordinatorStopOperations returns exact receipts so a repeated parent
// request can report completed stops after the original response was lost.
func (r *Repository) ListCoordinatorStopOperations(ctx context.Context, taskID string) ([]*models.CoordinatorStopOperation, error) {
	rows, err := r.ro.QueryxContext(ctx, r.ro.Rebind(`
		SELECT id, task_id, session_id, turn_id, execution_id, agentctl_generation, executor_status, executor_updated_at, admission_cutoff, status, reason_code, proof_scope, created_at, updated_at
		FROM task_stop_operations
		WHERE task_id = ?
		ORDER BY created_at ASC, id ASC
	`), taskID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var operations []*models.CoordinatorStopOperation
	for rows.Next() {
		operation := &models.CoordinatorStopOperation{}
		if err := rows.Scan(&operation.ID, &operation.TaskID, &operation.SessionID, &operation.TurnID, &operation.ExecutionID, &operation.AgentctlGeneration, &operation.ExecutorStatus, &operation.ExecutorUpdatedAt, &operation.AdmissionCutoff, &operation.Status, &operation.ReasonCode, &operation.ProofScope, &operation.CreatedAt, &operation.UpdatedAt); err != nil {
			return nil, err
		}
		operations = append(operations, operation)
	}
	return operations, rows.Err()
}

// FenceCoordinatorStopSession settles the session and leaves a durable launch
// tombstone when no exact execution incarnation was available to capture.
func (r *Repository) FenceCoordinatorStopSession(ctx context.Context, taskID, sessionID string) (bool, error) {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := kandevdb.LockTaskRowInTx(ctx, tx, r.db.DriverName(), taskID); err != nil {
		return false, err
	}
	if err := lockSessionTurnWrites(ctx, tx, r.db.DriverName(), sessionID); err != nil {
		return false, err
	}
	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx, r.db.Rebind(`UPDATE task_sessions SET state = ?, completed_at = ?, updated_at = ? WHERE id = ? AND task_id = ? AND state IN ('CREATED', 'STARTING', 'RUNNING', 'WAITING_FOR_INPUT')`), string(models.TaskSessionStateCancelled), now, now, sessionID, taskID)
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if changed == 1 {
		var turnID string
		turnErr := tx.QueryRowContext(ctx, r.db.Rebind(`SELECT id FROM task_session_turns WHERE task_session_id = ? AND completed_at IS NULL ORDER BY started_at DESC LIMIT 1`), sessionID).Scan(&turnID)
		if turnErr != nil && !errors.Is(turnErr, sql.ErrNoRows) {
			return false, turnErr
		}
		if turnID != "" {
			if _, err := tx.ExecContext(ctx, r.db.Rebind(`UPDATE task_session_turns SET completed_at = ?, updated_at = ? WHERE id = ? AND task_session_id = ? AND completed_at IS NULL`), now, now, turnID, sessionID); err != nil {
				return false, err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, r.db.Rebind(`INSERT INTO task_stop_session_fences(task_id, session_id, created_at) VALUES (?, ?, ?) ON CONFLICT(task_id, session_id) DO NOTHING`), taskID, sessionID, now); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return changed == 1, nil
}

func (r *Repository) ListCoordinatorStopSessionFences(ctx context.Context, taskID string) ([]models.CoordinatorStopSessionFenceReceipt, error) {
	rows, err := r.ro.QueryxContext(ctx, r.ro.Rebind(`SELECT task_id, session_id, created_at FROM task_stop_session_fences WHERE task_id = ? ORDER BY created_at ASC, session_id ASC`), taskID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var receipts []models.CoordinatorStopSessionFenceReceipt
	for rows.Next() {
		var receipt models.CoordinatorStopSessionFenceReceipt
		if err := rows.Scan(&receipt.TaskID, &receipt.SessionID, &receipt.CreatedAt); err != nil {
			return nil, err
		}
		receipt.Status = models.CoordinatorStopOperationStatusIncomplete
		receipt.ProofScope = "session_launch_fenced_without_execution_identity"
		receipts = append(receipts, receipt)
	}
	return receipts, rows.Err()
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
	proofScope := models.CoordinatorStopProofScopeProcessesDrained
	if !receipt.ManagedProcessesDrained {
		reasonCode = "managed_processes_not_drained"
		proofScope = models.CoordinatorStopProofScopeAgentctlFence
	}
	result, err := tx.ExecContext(ctx, db.Rebind(`UPDATE task_stop_operations SET admission_cutoff = ?, status = ?, reason_code = ?, proof_scope = ?, updated_at = ? WHERE id = ? AND execution_id = ? AND agentctl_generation = ? AND status IN (?, ?)`), receipt.AdmissionClosedAt, models.CoordinatorStopOperationStatusIncomplete, reasonCode, proofScope, time.Now().UTC(), operationID, receipt.ExecutionID, receipt.AgentctlGeneration, models.CoordinatorStopOperationStatusFencing, models.CoordinatorStopOperationStatusIncomplete)
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

// RecordCoordinatorStopLifecycleProof records that lifecycle completed the
// graceful stop for the same execution after agentctl closed admission and
// drained its managed process tree.
func (r *Repository) RecordCoordinatorStopLifecycleProof(ctx context.Context, operationID, executionID string, agentctlGeneration uint64) error {
	if operationID == "" || executionID == "" || agentctlGeneration == 0 {
		return errors.New("lifecycle stop proof requires operation, execution, and agentctl generation")
	}
	result, err := r.db.ExecContext(ctx, r.db.Rebind(`UPDATE task_stop_operations SET proof_scope = ?, reason_code = 'executor_terminal_proof_pending', updated_at = ? WHERE id = ? AND execution_id = ? AND agentctl_generation = ? AND status = ? AND proof_scope = ?`), models.CoordinatorStopProofScopeLifecycleTerminal, time.Now().UTC(), operationID, executionID, agentctlGeneration, models.CoordinatorStopOperationStatusIncomplete, models.CoordinatorStopProofScopeProcessesDrained)
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

// FinalizeCoordinatorStopOperation promotes an exact receipt only after the
// captured turn remains closed and its executor row is absent or terminal.
// A successor row or any live captured row fails closed.
//
//nolint:cyclop // The terminal proof checks are one atomic transaction.
func (r *Repository) FinalizeCoordinatorStopOperation(ctx context.Context, operationID, executionID string, agentctlGeneration uint64) (*models.CoordinatorStopOperation, error) {
	if operationID == "" || executionID == "" || agentctlGeneration == 0 {
		return nil, errors.New("finalize coordinator stop requires operation, execution, and agentctl generation")
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin coordinator stop finalization: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	op, found, err := getCoordinatorStopOperationTx(ctx, tx, r.db, operationID)
	if err != nil || !found || op.ExecutionID != executionID || op.AgentctlGeneration != agentctlGeneration {
		return nil, models.ErrExecutionRotated
	}
	if err := kandevdb.LockTaskRowInTx(ctx, tx, r.db.DriverName(), op.TaskID); err != nil {
		return nil, err
	}
	if err := lockSessionTurnWrites(ctx, tx, r.db.DriverName(), op.SessionID); err != nil {
		return nil, err
	}
	if err := validateCoordinatorStopTerminalBoundaryTx(ctx, tx, r.db, op); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx, r.db.Rebind(`UPDATE task_stop_operations SET status = ?, reason_code = '', proof_scope = ?, updated_at = ? WHERE id = ? AND execution_id = ? AND agentctl_generation = ? AND status = ?`), models.CoordinatorStopOperationStatusStopped, "exact_executor_terminal", now, op.ID, executionID, agentctlGeneration, models.CoordinatorStopOperationStatusIncomplete)
	if err != nil {
		return nil, err
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return nil, models.ErrExecutionRotated
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return r.GetCoordinatorStopOperation(ctx, operationID)
}

func validateCoordinatorStopTerminalBoundaryTx(ctx context.Context, tx *sqlx.Tx, db *sqlx.DB, op *models.CoordinatorStopOperation) error {
	if op.Status != models.CoordinatorStopOperationStatusIncomplete || op.ProofScope != models.CoordinatorStopProofScopeLifecycleTerminal || op.AdmissionCutoff.IsZero() {
		return models.ErrExecutionRotated
	}
	var completed, successorOpen, cancelled bool
	if err := tx.GetContext(ctx, &completed, db.Rebind(`SELECT EXISTS(SELECT 1 FROM task_session_turns WHERE id = ? AND task_session_id = ? AND task_id = ? AND completed_at IS NOT NULL)`), op.TurnID, op.SessionID, op.TaskID); err != nil || !completed {
		return models.ErrExecutionRotated
	}
	if err := tx.GetContext(ctx, &successorOpen, db.Rebind(`SELECT EXISTS(SELECT 1 FROM task_session_turns WHERE task_session_id = ? AND task_id = ? AND id <> ? AND completed_at IS NULL)`), op.SessionID, op.TaskID, op.TurnID); err != nil || successorOpen {
		return models.ErrExecutionRotated
	}
	if err := tx.GetContext(ctx, &cancelled, db.Rebind(`SELECT EXISTS(SELECT 1 FROM task_sessions WHERE id = ? AND task_id = ? AND state = ?)`), op.SessionID, op.TaskID, string(models.TaskSessionStateCancelled)); err != nil || !cancelled {
		return models.ErrExecutionRotated
	}
	var currentExecutionID, status string
	var generation uint64
	err := tx.QueryRowxContext(ctx, db.Rebind(`SELECT agent_execution_id, agentctl_generation, status FROM executors_running WHERE session_id = ?`), op.SessionID).Scan(&currentExecutionID, &generation, &status)
	if errors.Is(err, sql.ErrNoRows) {
		// Lifecycle deletes the captured row after terminal teardown; its exact
		// proof was checked before this transaction can promote the operation.
		return nil
	}
	if err != nil || currentExecutionID != op.ExecutionID || generation != op.AgentctlGeneration || !terminalCoordinatorStopExecutorStatus(status) {
		return models.ErrExecutionRotated
	}
	return nil
}

func terminalCoordinatorStopExecutorStatus(status string) bool {
	return status == models.ExecutorRunningStatusStopped || status == models.ExecutorRunningStatusFailed || status == models.ExecutorRunningStatusComplete
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
