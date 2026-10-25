package coordinator

import (
	"context"
	"database/sql"
	"errors"

	"github.com/jmoiron/sqlx"

	"github.com/kandev/kandev/internal/coordinator/watch"
	"github.com/kandev/kandev/internal/db/dialect"
)

// DependencyMutationAllowedTx rechecks the live conversation and both endpoint
// scopes under the dependency writer's workspace transaction barrier.
func (s *Service) DependencyMutationAllowedTx(ctx context.Context, tx *sqlx.Tx,
	coordinatorID, workspaceID, callerTaskID, callerSessionID, callerExecutionID,
	dependentID, predecessorID string) error {
	if !validDependencyScopeInput(s, tx, coordinatorID, workspaceID, callerTaskID, callerSessionID,
		callerExecutionID, dependentID, predecessorID) {
		return ErrNotFound
	}
	lock := ""
	if dialect.IsPostgres(s.store.db.DriverName()) {
		lock = ` FOR UPDATE`
	}
	if err := s.dependencyCallerLiveTx(ctx, tx, workspaceID, callerTaskID, callerSessionID,
		callerExecutionID, lock); err != nil {
		return err
	}
	if err := s.dependencyConversationBoundTx(ctx, tx, coordinatorID, workspaceID, callerTaskID, lock); err != nil {
		return err
	}
	set, err := s.store.EffectiveWatchSet(ctx, tx, coordinatorID, workspaceID)
	if err != nil {
		return err
	}
	projects, err := watch.NewResolver(watchTxReader{tx: tx, db: s.store.db}).Resolve(ctx, workspaceID, set.Projects)
	if err != nil {
		return err
	}
	for _, id := range []string{dependentID, predecessorID} {
		if err := s.dependencyEndpointWatchedTx(ctx, tx, workspaceID, id, set, projects); err != nil {
			return err
		}
	}
	return nil
}

func validDependencyScopeInput(s *Service, tx *sqlx.Tx, ids ...string) bool {
	if s == nil || tx == nil {
		return false
	}
	for _, id := range ids {
		if id == "" {
			return false
		}
	}
	return true
}

func (s *Service) dependencyCallerLiveTx(ctx context.Context, tx *sqlx.Tx,
	workspaceID, callerTaskID, callerSessionID, callerExecutionID, lock string) error {
	var sessionState string
	if err := tx.QueryRowContext(ctx, s.store.db.Rebind(`SELECT ts.state FROM task_sessions ts
		JOIN tasks t ON t.id = ts.task_id WHERE ts.id = ? AND ts.task_id = ?
		AND t.workspace_id = ? AND t.archived_at IS NULL`+lock),
		callerSessionID, callerTaskID, workspaceID).Scan(&sessionState); err != nil {
		return err
	}
	if sessionState != "RUNNING" {
		return ErrNotFound
	}
	var executorStatus string
	if err := tx.QueryRowContext(ctx, s.store.db.Rebind(`SELECT status FROM executors_running
		WHERE session_id = ? AND task_id = ? AND agent_execution_id = ?`+lock),
		callerSessionID, callerTaskID, callerExecutionID).Scan(&executorStatus); err != nil {
		return err
	}
	if executorStatus != "running" && executorStatus != "ready" {
		return ErrNotFound
	}
	return nil
}

func (s *Service) dependencyConversationBoundTx(ctx context.Context, tx *sqlx.Tx,
	coordinatorID, workspaceID, callerTaskID, lock string) error {
	query := `SELECT conversation_task_id FROM coordinators WHERE id = ? AND workspace_id = ?` + lock
	var conversationTaskID sql.NullString
	if err := tx.QueryRowContext(ctx, s.store.db.Rebind(query), coordinatorID, workspaceID).Scan(&conversationTaskID); err != nil {
		return err
	}
	if !conversationTaskID.Valid || conversationTaskID.String != callerTaskID {
		return ErrNotFound
	}
	return nil
}

func (s *Service) dependencyEndpointWatchedTx(ctx context.Context, tx *sqlx.Tx,
	workspaceID, id string, set WatchSet, projects watch.Resolved) error {
	var taskWorkspace, workflowID string
	if err := tx.QueryRowContext(ctx, s.store.db.Rebind(`SELECT workspace_id, workflow_id FROM tasks WHERE id = ?`), id).
		Scan(&taskWorkspace, &workflowID); err != nil {
		return err
	}
	if taskWorkspace != workspaceID || !set.Contains(workflowID) {
		return ErrNotFound
	}
	var repos []string
	if projects.Selected() {
		if err := tx.SelectContext(ctx, &repos, s.store.db.Rebind(`SELECT r.id FROM task_repositories tr
				JOIN repositories r ON r.id = tr.repository_id AND r.workspace_id = ? AND r.deleted_at IS NULL
				WHERE tr.task_id = ?`), workspaceID, id); err != nil {
			return err
		}
	}
	if !watch.Task(true, projects, repos) {
		return ErrNotFound
	}
	return nil
}

// DependencyScopeDenied classifies a missing or out-of-scope target without
// exposing which endpoint failed.
func DependencyScopeDenied(err error) bool {
	return errors.Is(err, ErrNotFound) || errors.Is(err, sql.ErrNoRows)
}
