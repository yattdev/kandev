package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/kandev/kandev/internal/task/models"
)

const preparedWorkflowAttachFailure = "environment preparation failed: base branch does not exist"

// PromotePreparedWorkflowSession keeps the route/entry/source checks inside
// the same transaction that commits primary ownership after workspace attach.
func (r *Repository) PromotePreparedWorkflowSession(ctx context.Context, sessionID string, route models.WorkflowSessionRoute) (bool, error) {
	prepared := route
	prepared.Phase = "prepared"
	return r.setSessionPrimary(ctx, sessionID, true, &route, prepared)
}

func (r *Repository) preparedWorkflowPromotionCurrentTx(ctx context.Context, tx *sqlx.Tx, taskID string, route models.WorkflowSessionRoute) (bool, error) {
	valid, err := r.preparedWorkflowRouteCurrentTx(ctx, tx, taskID, route)
	if err != nil || !valid {
		return false, err
	}
	result, err := tx.ExecContext(ctx, tx.Rebind(`UPDATE task_sessions SET updated_at = updated_at
  WHERE id = ? AND task_id = ? AND is_primary = 1 AND state = 'WAITING_FOR_INPUT'`), route.SourceSessionID, taskID)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return false, err
	}
	var count int
	err = tx.QueryRowContext(ctx, tx.Rebind(`SELECT COUNT(*) FROM task_sessions dest JOIN task_sessions source
  ON source.id = ? AND source.task_id = dest.task_id
  WHERE dest.id = ? AND dest.task_id = ? AND dest.state = 'CREATED' AND dest.is_primary = 0
  AND dest.agent_profile_id = ? AND dest.is_passthrough = 0
  AND dest.task_environment_id = source.task_environment_id
  AND dest.executor_id = source.executor_id AND dest.executor_profile_id = source.executor_profile_id
  AND NOT EXISTS (SELECT 1 FROM task_session_turns WHERE task_session_id = dest.id)
  AND NOT EXISTS (SELECT 1 FROM agent_delivery_submissions WHERE session_id = dest.id)`),
		route.SourceSessionID, route.DestinationID, taskID, route.AgentProfileID).Scan(&count)
	return count == 1, err
}

// RearmPreparedWorkflowSession changes only a recorded pre-first-turn attach
// failure. All ownership reads and both state writes share the runtime/turn
// admission locks and one transaction. CREATED routes are validated as well.
func (r *Repository) RearmPreparedWorkflowSession(ctx context.Context, taskID string, route models.WorkflowSessionRoute) (bool, error) {
	if !validPreparedWorkflowRearmRoute(route) {
		return false, nil
	}
	tx, err := r.db.BeginTxx(ctx, r.hierarchyTxOptions())
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := r.managedDeletionBarrierTx(ctx, tx, taskID); err != nil {
		return false, err
	}
	if err := lockSessionTurnWrites(ctx, tx, r.db.DriverName(), route.DestinationID); err != nil {
		return false, err
	}
	if err := lockAgentDeliveryWrites(ctx, tx, r.db.DriverName(), []string{route.DestinationID}, nil); err != nil {
		return false, err
	}
	valid, err := r.preparedWorkflowRouteCurrentTx(ctx, tx, taskID, route)
	if err != nil || !valid {
		return false, err
	}
	result, err := tx.ExecContext(ctx, tx.Rebind(preparedWorkflowRearmQuery), time.Now().UTC(),
		route.DestinationID, taskID, route.AgentProfileID, preparedWorkflowAttachFailure, route.SourceSessionID, taskID)
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return false, err
	}
	_, err = tx.ExecContext(ctx, tx.Rebind(`UPDATE tasks SET state = 'SCHEDULING', updated_at = ? WHERE id = ?`),
		time.Now().UTC(), taskID)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func validPreparedWorkflowRearmRoute(route models.WorkflowSessionRoute) bool {
	return route.Phase == "prepared" && route.TargetKind == "profile" && route.TargetStepID == "" &&
		route.SourceSessionID != "" && route.DestinationID != "" && route.SourceSessionID != route.DestinationID
}

func (r *Repository) preparedWorkflowRouteCurrentTx(ctx context.Context, tx *sqlx.Tx, taskID string, route models.WorkflowSessionRoute) (bool, error) {
	_, step, found, err := r.readTaskStepInTx(ctx, tx, taskID)
	if err != nil || !found || step != route.DestinationStepID {
		return false, err
	}
	var raw sql.NullString
	var state string
	var archived sql.NullTime
	if err := tx.QueryRowContext(ctx, tx.Rebind(`SELECT metadata, state, archived_at FROM tasks WHERE id = ?`),
		taskID).Scan(&raw, &state, &archived); err != nil {
		return false, err
	}
	if archived.Valid || (state != "SCHEDULING" && state != "FAILED") {
		return false, nil
	}
	var metadata map[string]interface{}
	if err := json.Unmarshal([]byte(raw.String), &metadata); err != nil {
		return false, err
	}
	current, ok := models.LoadWorkflowSessionRoute(metadata)
	if !ok || current != route {
		return false, nil
	}
	var id int64
	var from, to sql.NullString
	err = tx.QueryRowContext(ctx, tx.Rebind(`SELECT id, from_workflow_step_id, to_workflow_step_id
  FROM task_step_transitions WHERE task_id = ? ORDER BY id DESC LIMIT 1`), taskID).Scan(&id, &from, &to)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return from.Valid && to.String == step && route.EntryIdentity == fmt.Sprintf("entry:%020d", id), nil
}

const preparedWorkflowRearmQuery = `
 UPDATE task_sessions SET state = 'CREATED', error_message = '', completed_at = NULL, updated_at = ?
 WHERE id = ? AND task_id = ? AND agent_profile_id = ? AND is_primary = 0 AND is_passthrough = 0
 AND (state = 'CREATED' OR (state = 'FAILED' AND error_message = ?))
 AND downstream_acp_session_id = '' AND route_generation = 0
 AND EXISTS (SELECT 1 FROM task_sessions source JOIN task_environments env ON env.id = source.task_environment_id
  WHERE source.id = ? AND source.task_id = ? AND source.is_primary = 1 AND source.state = 'WAITING_FOR_INPUT'
  AND source.task_environment_id = task_sessions.task_environment_id AND env.status = 'ready'
  AND source.executor_id = task_sessions.executor_id AND source.executor_profile_id = task_sessions.executor_profile_id)
 AND NOT EXISTS (SELECT 1 FROM task_session_turns WHERE task_session_id = task_sessions.id)
 AND NOT EXISTS (SELECT 1 FROM executors_running WHERE session_id = task_sessions.id)
 AND NOT EXISTS (SELECT 1 FROM harness_session_generations WHERE session_id = task_sessions.id)
 AND NOT EXISTS (SELECT 1 FROM agent_delivery_submissions WHERE session_id = task_sessions.id)
 AND NOT EXISTS (SELECT 1 FROM session_recovery_blocks WHERE session_id = task_sessions.id)
 `
