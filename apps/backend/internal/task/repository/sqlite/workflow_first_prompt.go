package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/kandev/kandev/internal/task/models"
)

// ValidatePendingWorkflowFirstPrompt completes only a recovery-owned CREATED
// destination. It never rearms a committed failure or an existing conversation.
func (r *Repository) ValidatePendingWorkflowFirstPrompt(ctx context.Context, taskID string, route models.WorkflowSessionRoute) (bool, error) {
	if route.Phase != committedWorkflowRoutePhase || !route.FirstPromptPending || route.SourceSessionID == route.DestinationID {
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
	valid, err := r.preparedWorkflowRouteCurrentTx(ctx, tx, taskID, route)
	if err != nil || !valid {
		return false, err
	}
	var raw sql.NullString
	err = tx.QueryRowContext(ctx, tx.Rebind(pendingWorkflowFirstPromptQuery),
		route.DestinationID, taskID, route.AgentProfileID, route.SourceSessionID).Scan(&raw)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var metadata map[string]interface{}
	if err := json.Unmarshal([]byte(raw.String), &metadata); err != nil {
		return false, err
	}
	_, initialExists, err := models.LoadInitialPromptSubmission(metadata)
	if err != nil || initialExists {
		return false, err
	}
	return true, tx.Commit()
}

func (r *Repository) CompletePendingWorkflowFirstPrompt(ctx context.Context, taskID string, expected models.WorkflowSessionRoute) error {
	tx, err := r.db.BeginTxx(ctx, r.hierarchyTxOptions())
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, _, _, err := r.readTaskStepInTx(ctx, tx, taskID); err != nil {
		return err
	}
	var raw sql.NullString
	if err := tx.QueryRowContext(ctx, tx.Rebind(`SELECT metadata FROM tasks WHERE id = ?`), taskID).Scan(&raw); err != nil {
		return err
	}
	var metadata map[string]interface{}
	if err := json.Unmarshal([]byte(raw.String), &metadata); err != nil {
		return err
	}
	route, ok := models.LoadWorkflowSessionRoute(metadata)
	if !ok || !route.FirstPromptPending || route.Phase != committedWorkflowRoutePhase || route.OperationID != expected.OperationID ||
		route.EntryIdentity != expected.EntryIdentity || route.DestinationID != expected.DestinationID {
		return nil
	}
	var owned bool
	if err := tx.QueryRowContext(ctx, tx.Rebind(`SELECT EXISTS (SELECT 1 FROM task_session_turns WHERE task_session_id = ?)
  OR EXISTS (SELECT 1 FROM agent_delivery_submissions WHERE session_id = ?)`), route.DestinationID, route.DestinationID).Scan(&owned); err != nil {
		return err
	}
	if !owned {
		return nil
	}
	route.FirstPromptPending = false
	if err := r.setTaskMetadataKeyWithExecutor(ctx, tx, taskID, models.MetaKeyWorkflowSessionRoute, route, time.Now().UTC()); err != nil {
		return err
	}
	return tx.Commit()
}

const pendingWorkflowFirstPromptQuery = `
 SELECT dest.metadata FROM task_sessions dest JOIN task_sessions source ON source.task_id = dest.task_id
 JOIN tasks task ON task.id = dest.task_id JOIN task_environments env ON env.id = dest.task_environment_id
 WHERE dest.id = ? AND dest.task_id = ? AND dest.agent_profile_id = ? AND source.id = ?
 AND task.state = 'SCHEDULING' AND dest.is_primary = 1 AND source.is_primary = 0
 AND dest.state = 'CREATED' AND dest.is_passthrough = 0 AND dest.route_generation = 0 AND dest.downstream_acp_session_id = ''
 AND source.state IN ('WAITING_FOR_INPUT', 'COMPLETED') AND env.status = 'ready'
 AND source.task_environment_id = dest.task_environment_id
 AND source.executor_id = dest.executor_id AND source.executor_profile_id = dest.executor_profile_id
 AND NOT EXISTS (SELECT 1 FROM task_session_turns WHERE task_session_id = dest.id)
 AND NOT EXISTS (SELECT 1 FROM harness_session_generations WHERE session_id = dest.id)
 AND NOT EXISTS (SELECT 1 FROM agent_delivery_submissions WHERE session_id = dest.id)
 AND NOT EXISTS (SELECT 1 FROM session_recovery_blocks WHERE session_id = dest.id)
 AND NOT EXISTS (SELECT 1 FROM executors_running WHERE session_id = dest.id AND COALESCE(resume_token, '') <> '')
 AND NOT EXISTS (SELECT 1 FROM task_session_turns turn WHERE turn.task_session_id = source.id
  AND turn.started_at > (SELECT occurred_at FROM task_step_transitions WHERE task_id = task.id ORDER BY id DESC LIMIT 1))
 `
