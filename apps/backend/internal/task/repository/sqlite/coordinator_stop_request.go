package sqlite

import (
	"context"
	"fmt"
	"time"

	kandevdb "github.com/kandev/kandev/internal/db"
	"github.com/kandev/kandev/internal/task/models"
)

// CaptureCoordinatorStopRequest creates one parent-bound caller operation ID.
// A repeated key is accepted only for the same task and parent.
func (r *Repository) CaptureCoordinatorStopRequest(ctx context.Context, request models.CoordinatorStopRequest) (*models.CoordinatorStopRequest, bool, error) {
	got, _, created, err := r.CaptureCoordinatorStopRequestWithCandidates(ctx, request)
	return got, created, err
}

// CaptureCoordinatorStopRequestWithCandidates creates a caller operation and
// its immutable active-session inventory under the same task lock. A retry
// returns the original inventory, including a durable empty inventory.
//
//nolint:cyclop,nestif // The single transaction is the atomicity boundary for request and inventory.
func (r *Repository) CaptureCoordinatorStopRequestWithCandidates(ctx context.Context, request models.CoordinatorStopRequest) (*models.CoordinatorStopRequest, []string, bool, error) {
	if request.TaskID == "" || request.OperationID == "" || request.ParentTaskID == "" {
		return nil, nil, false, fmt.Errorf("coordinator stop request requires task, operation, and parent")
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, nil, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := kandevdb.LockTaskRowInTx(ctx, tx, r.db.DriverName(), request.TaskID); err != nil {
		return nil, nil, false, err
	}
	now := time.Now().UTC()
	request.CreatedAt, request.UpdatedAt = now, now
	result, err := tx.ExecContext(ctx, r.db.Rebind(`INSERT INTO task_stop_requests (task_id, operation_id, parent_task_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?) ON CONFLICT(task_id, operation_id) DO NOTHING`), request.TaskID, request.OperationID, request.ParentTaskID, now, now)
	if err != nil {
		return nil, nil, false, err
	}
	created, err := result.RowsAffected()
	if err != nil {
		return nil, nil, false, err
	}
	var got models.CoordinatorStopRequest
	err = tx.GetContext(ctx, &got, r.db.Rebind(`SELECT task_id, operation_id, parent_task_id, complete, result_status, created_at, updated_at FROM task_stop_requests WHERE task_id = ? AND operation_id = ?`), request.TaskID, request.OperationID)
	if err != nil {
		return nil, nil, false, err
	}
	if got.ParentTaskID != request.ParentTaskID {
		return nil, nil, false, fmt.Errorf("coordinator stop request ID is bound to a different parent")
	}
	var captured bool
	if err := tx.GetContext(ctx, &captured, r.db.Rebind(`SELECT candidates_captured FROM task_stop_requests WHERE task_id = ? AND operation_id = ?`), request.TaskID, request.OperationID); err != nil {
		return nil, nil, false, err
	}
	if !captured {
		if r.coordinatorStopRequestBeforeCandidateSnapshot != nil {
			if err := r.coordinatorStopRequestBeforeCandidateSnapshot(); err != nil {
				return nil, nil, false, err
			}
		}
		var sessionIDs []string
		if err := tx.SelectContext(ctx, &sessionIDs, r.db.Rebind(`SELECT id FROM task_sessions WHERE task_id = ? AND state IN ('CREATED', 'STARTING', 'RUNNING', 'WAITING_FOR_INPUT') ORDER BY started_at DESC`), request.TaskID); err != nil {
			return nil, nil, false, err
		}
		for _, sessionID := range sessionIDs {
			if _, err := tx.ExecContext(ctx, r.db.Rebind(`INSERT INTO task_stop_request_candidates(task_id, operation_id, session_id) VALUES (?, ?, ?)`), request.TaskID, request.OperationID, sessionID); err != nil {
				return nil, nil, false, err
			}
		}
		if _, err := tx.ExecContext(ctx, r.db.Rebind(`UPDATE task_stop_requests SET candidates_captured = TRUE, updated_at = ? WHERE task_id = ? AND operation_id = ?`), now, request.TaskID, request.OperationID); err != nil {
			return nil, nil, false, err
		}
	}
	var candidates []string
	if err := tx.SelectContext(ctx, &candidates, r.db.Rebind(`SELECT session_id FROM task_stop_request_candidates WHERE task_id = ? AND operation_id = ? ORDER BY session_id`), request.TaskID, request.OperationID); err != nil {
		return nil, nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, false, err
	}
	return &got, candidates, created == 1, nil
}

func (r *Repository) CompleteCoordinatorStopRequest(ctx context.Context, taskID, operationID, parentTaskID, status string) error {
	result, err := r.db.ExecContext(ctx, r.db.Rebind(`UPDATE task_stop_requests SET complete = ?, result_status = ?, updated_at = ? WHERE task_id = ? AND operation_id = ? AND parent_task_id = ?`), true, status, time.Now().UTC(), taskID, operationID, parentTaskID)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return fmt.Errorf("coordinator stop request is not bound to task and parent")
	}
	return nil
}

func (r *Repository) GetCoordinatorStopRequest(ctx context.Context, taskID, operationID, parentTaskID string) (*models.CoordinatorStopRequest, error) {
	var request models.CoordinatorStopRequest
	err := r.db.GetContext(ctx, &request, r.db.Rebind(`SELECT task_id, operation_id, parent_task_id, complete, result_status, created_at, updated_at FROM task_stop_requests WHERE task_id = ? AND operation_id = ? AND parent_task_id = ?`), taskID, operationID, parentTaskID)
	if err != nil {
		return nil, err
	}
	return &request, nil
}

func (r *Repository) BindCoordinatorStopRequestReceipts(ctx context.Context, taskID, operationID string, receiptIDs []string) error {
	if len(receiptIDs) == 0 {
		return nil
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, receiptID := range receiptIDs {
		result, err := tx.ExecContext(ctx, r.db.Rebind(`INSERT INTO task_stop_request_receipts (task_id, operation_id, receipt_id) SELECT ?, ?, ? WHERE EXISTS (SELECT 1 FROM task_stop_requests WHERE task_id = ? AND operation_id = ?) AND EXISTS (SELECT 1 FROM task_stop_operations WHERE id = ? AND task_id = ?) ON CONFLICT(task_id, operation_id, receipt_id) DO NOTHING`), taskID, operationID, receiptID, taskID, operationID, receiptID, taskID)
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed == 0 {
			var bound bool
			if err := tx.GetContext(ctx, &bound, r.db.Rebind(`SELECT EXISTS (SELECT 1 FROM task_stop_request_receipts WHERE task_id = ? AND operation_id = ? AND receipt_id = ?)`), taskID, operationID, receiptID); err != nil || !bound {
				return fmt.Errorf("coordinator stop request or exact receipt is not bound to task")
			}
		}
	}
	return tx.Commit()
}

func (r *Repository) BindCoordinatorStopRequestReceipt(ctx context.Context, taskID, operationID, receiptID string) error {
	result, err := r.db.ExecContext(ctx, r.db.Rebind(`INSERT INTO task_stop_request_receipts (task_id, operation_id, receipt_id) SELECT ?, ?, ? WHERE EXISTS (SELECT 1 FROM task_stop_requests WHERE task_id = ? AND operation_id = ?) AND EXISTS (SELECT 1 FROM task_stop_operations WHERE id = ? AND task_id = ?)`), taskID, operationID, receiptID, taskID, operationID, receiptID, taskID)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		return fmt.Errorf("coordinator stop request or exact receipt is not bound to task")
	}
	return nil
}

// ListCoordinatorStopRequestReceipts returns only receipts bound to the exact
// parent-owned request; it never falls back to task-wide receipt inventory.
func (r *Repository) ListCoordinatorStopRequestReceipts(ctx context.Context, taskID, operationID, parentTaskID string) ([]models.CoordinatorStopOperation, error) {
	var receipts []models.CoordinatorStopOperation
	err := r.db.SelectContext(ctx, &receipts, r.db.Rebind(`SELECT o.* FROM task_stop_operations o JOIN task_stop_request_receipts b ON b.receipt_id = o.id JOIN task_stop_requests q ON q.task_id = b.task_id AND q.operation_id = b.operation_id WHERE q.task_id = ? AND q.operation_id = ? AND q.parent_task_id = ? ORDER BY o.session_id`), taskID, operationID, parentTaskID)
	return receipts, err
}
