package sqlite

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/kandev/kandev/internal/db/dialect"
	"github.com/kandev/kandev/internal/task/models"
)

var (
	ErrForceRemovalGrantStale = errors.New("force removal grant is stale")
	ErrForceRemovalGrantUsed  = errors.New("force removal grant is already consumed")
)

// IssueForceRemovalGrant persists one server-generated, expiring exact-target grant.
func (r *Repository) IssueForceRemovalGrant(ctx context.Context, grant *models.ForceRemovalGrant) (*models.ForceRemovalGrant, error) {
	if !validForceRemovalGrant(grant) {
		return nil, ErrForceRemovalGrantStale
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	query := `SELECT updated_at FROM tasks WHERE id = ? AND workspace_id = ?`
	if dialect.IsPostgres(r.db.DriverName()) {
		query += forUpdateClause
	}
	var generation time.Time
	if err := tx.QueryRowContext(ctx, r.db.Rebind(query), grant.TaskID, grant.WorkspaceID).Scan(&generation); err != nil || !generation.Equal(grant.TaskGeneration) {
		return nil, ErrForceRemovalGrantStale
	}
	now := time.Now().UTC()
	if !grant.ExpiresAt.After(now) {
		return nil, ErrForceRemovalGrantStale
	}
	grant.ID, grant.CreatedAt = uuid.NewString(), now
	_, err = tx.ExecContext(ctx, r.db.Rebind(`INSERT INTO task_force_removal_grants (id, task_id, workspace_id, task_generation, caller_task_id, caller_session_id, issued_by_user_id, expires_at, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`), grant.ID, grant.TaskID, grant.WorkspaceID, grant.TaskGeneration, grant.CallerTaskID, grant.CallerSessionID, grant.IssuedByUserID, grant.ExpiresAt, grant.CreatedAt)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return grant, nil
}

func validForceRemovalGrant(grant *models.ForceRemovalGrant) bool {
	return grant != nil && grant.TaskID != "" && grant.WorkspaceID != "" &&
		!grant.TaskGeneration.IsZero() && grant.CallerTaskID != "" &&
		grant.CallerSessionID != "" && grant.IssuedByUserID != "" && !grant.ExpiresAt.IsZero()
}

// ConsumeForceRemovalGrant atomically checks the exact caller and target snapshot.
func (r *Repository) ConsumeForceRemovalGrant(ctx context.Context, id, taskID, workspaceID, callerTaskID, callerSessionID string, generation time.Time) (*models.ForceRemovalGrant, error) {
	now := time.Now().UTC()
	result, err := r.db.ExecContext(ctx, r.db.Rebind(`UPDATE task_force_removal_grants SET consumed_at = ? WHERE id = ? AND task_id = ? AND workspace_id = ? AND caller_task_id = ? AND caller_session_id = ? AND task_generation = ? AND consumed_at IS NULL AND expires_at > ?`), now, id, taskID, workspaceID, callerTaskID, callerSessionID, generation, now)
	if err != nil {
		return nil, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if changed != 1 {
		return nil, ErrForceRemovalGrantUsed
	}
	return &models.ForceRemovalGrant{ID: id, TaskID: taskID, WorkspaceID: workspaceID, TaskGeneration: generation, CallerTaskID: callerTaskID, CallerSessionID: callerSessionID, ConsumedAt: &now}, nil
}
