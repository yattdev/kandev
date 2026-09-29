package sqlite

import (
	"context"
	"fmt"
	"time"

	"github.com/kandev/kandev/internal/task/models"
)

// CaptureCoordinatorStopRequest creates one parent-bound caller operation ID.
// A repeated key is accepted only for the same task and parent.
func (r *Repository) CaptureCoordinatorStopRequest(ctx context.Context, request models.CoordinatorStopRequest) (*models.CoordinatorStopRequest, bool, error) {
	if request.TaskID == "" || request.OperationID == "" || request.ParentTaskID == "" {
		return nil, false, fmt.Errorf("coordinator stop request requires task, operation, and parent")
	}
	now := time.Now().UTC()
	request.CreatedAt, request.UpdatedAt = now, now
	result, err := r.db.ExecContext(ctx, r.db.Rebind(`INSERT INTO task_stop_requests (task_id, operation_id, parent_task_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?) ON CONFLICT(task_id, operation_id) DO NOTHING`), request.TaskID, request.OperationID, request.ParentTaskID, now, now)
	if err != nil {
		return nil, false, err
	}
	created, err := result.RowsAffected()
	if err != nil {
		return nil, false, err
	}
	var got models.CoordinatorStopRequest
	err = r.db.GetContext(ctx, &got, r.db.Rebind(`SELECT task_id, operation_id, parent_task_id, created_at, updated_at FROM task_stop_requests WHERE task_id = ? AND operation_id = ?`), request.TaskID, request.OperationID)
	if err != nil {
		return nil, false, err
	}
	if got.ParentTaskID != request.ParentTaskID {
		return nil, false, fmt.Errorf("coordinator stop request ID is bound to a different parent")
	}
	return &got, created == 1, nil
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
