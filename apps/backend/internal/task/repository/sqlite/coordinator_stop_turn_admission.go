package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/kandev/kandev/internal/db"
	"github.com/kandev/kandev/internal/task/models"
)

// lockCoordinatorTurnAdmissionTx shares the snapshot's task lock before any
// session lock, so a fresh launch cannot reserve its first turn after capture.
func (r *Repository) lockCoordinatorTurnAdmissionTx(ctx context.Context, tx *sqlx.Tx, turn *models.Turn) error {
	if err := db.LockTaskRowInTx(ctx, tx, r.db.DriverName(), turn.TaskID); err != nil {
		return err
	}
	if turn.CompletedAt != nil {
		return nil
	}
	flag := fmt.Sprint(turn.Metadata[models.TurnMetaKeyLifecycleOnly])
	if flag == "true" || flag == "1" {
		return nil
	}
	var found int
	err := tx.QueryRowContext(ctx, r.db.Rebind(`SELECT 1 FROM task_stop_request_candidates c
		JOIN task_stop_requests q ON q.task_id = c.task_id AND q.operation_id = c.operation_id
		JOIN task_sessions s ON s.id = c.session_id AND s.task_id = c.task_id
		WHERE c.session_id = ? AND c.task_id = ? AND q.complete = ?
			AND c.queue_incarnation_id <> '' AND c.queue_incarnation_id = s.queue_incarnation_id
			AND c.turn_id = '' AND c.execution_id = '' AND c.agentctl_generation = 0 LIMIT 1`), turn.TaskSessionID, turn.TaskID, false).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("check coordinator turn admission: %w", err)
	}
	return models.ErrExecutionStopFenced
}

func (r *Repository) beginConversationTurnMutation(ctx context.Context, turn *models.Turn) (*sqlx.Tx, int64, error) {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, 0, err
	}
	if err := r.lockCoordinatorTurnAdmissionTx(ctx, tx, turn); err != nil {
		_ = tx.Rollback()
		return nil, 0, err
	}
	base, err := r.prepareConversationMutation(ctx, tx, turn.TaskSessionID)
	if err != nil {
		_ = tx.Rollback()
		return nil, 0, err
	}
	return tx, base, nil
}
