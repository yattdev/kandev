package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/kandev/kandev/internal/db/dialect"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository/repoerrors"
)

const (
	exactTaskSnapshotDefaultTTL = 5 * time.Minute
	exactTaskSnapshotMaxTTL     = 15 * time.Minute
	exactTaskSnapshotMaxRows    = 500
	exactTaskSnapshotMaxPage    = 100
	exactTaskSnapshotCleanupMax = 100
)

func (r *Repository) initExactTaskSnapshotSchema() error {
	driver := r.db.DriverName()
	if err := r.migrate.Apply("exact_task_snapshots.tables", `
		CREATE TABLE IF NOT EXISTS exact_task_workspace_fences (
			workspace_id TEXT PRIMARY KEY,
			revision BIGINT NOT NULL DEFAULT 0
		);
		CREATE TABLE IF NOT EXISTS exact_task_snapshots (
			token TEXT PRIMARY KEY,
			workspace_id TEXT NOT NULL,
			include_archived INTEGER NOT NULL,
			include_ephemeral INTEGER NOT NULL,
			workspace_revision BIGINT NOT NULL,
			expires_at TIMESTAMP NOT NULL
		);
		CREATE TABLE IF NOT EXISTS exact_task_snapshot_rows (
			snapshot_token TEXT NOT NULL,
			ordinal INTEGER NOT NULL,
			task_id TEXT NOT NULL,
			workspace_id TEXT NOT NULL,
			workflow_id TEXT NOT NULL,
			workflow_step_id TEXT NOT NULL,
			title TEXT NOT NULL,
			description TEXT NOT NULL,
			state TEXT NOT NULL,
			priority TEXT NOT NULL,
			position INTEGER NOT NULL,
			archived INTEGER NOT NULL,
			resource_version BIGINT NOT NULL,
			PRIMARY KEY (snapshot_token, ordinal),
			UNIQUE (snapshot_token, task_id),
			FOREIGN KEY (snapshot_token) REFERENCES exact_task_snapshots(token) ON DELETE CASCADE
		);
		CREATE INDEX IF NOT EXISTS idx_exact_task_snapshots_expiry ON exact_task_snapshots(expires_at);
	`); err != nil {
		return fmt.Errorf("create exact task snapshot tables: %w", err)
	}
	if dialect.IsPostgres(driver) {
		if err := r.migrate.Apply("exact_task_snapshots.fence_function", `CREATE OR REPLACE FUNCTION kandev_exact_task_workspace_fence() RETURNS trigger AS $$ BEGIN IF TG_OP = 'DELETE' THEN INSERT INTO exact_task_workspace_fences(workspace_id, revision) VALUES (OLD.workspace_id, 1) ON CONFLICT(workspace_id) DO UPDATE SET revision = exact_task_workspace_fences.revision + 1; ELSE INSERT INTO exact_task_workspace_fences(workspace_id, revision) VALUES (NEW.workspace_id, 1) ON CONFLICT(workspace_id) DO UPDATE SET revision = exact_task_workspace_fences.revision + 1; IF TG_OP = 'UPDATE' AND NEW.workspace_id <> OLD.workspace_id THEN INSERT INTO exact_task_workspace_fences(workspace_id, revision) VALUES (OLD.workspace_id, 1) ON CONFLICT(workspace_id) DO UPDATE SET revision = exact_task_workspace_fences.revision + 1; END IF; END IF; RETURN NULL; END; $$ LANGUAGE plpgsql`); err != nil {
			return fmt.Errorf("create exact task snapshot fence function: %w", err)
		}
		return r.migrate.Apply("exact_task_snapshots.fence_trigger", `CREATE TRIGGER exact_task_workspace_fence_trigger AFTER INSERT OR UPDATE OR DELETE ON tasks FOR EACH ROW EXECUTE FUNCTION kandev_exact_task_workspace_fence()`)
	}
	return r.migrate.Apply("exact_task_snapshots.fence_trigger", `CREATE TRIGGER exact_task_workspace_fence_insert AFTER INSERT ON tasks BEGIN INSERT INTO exact_task_workspace_fences(workspace_id, revision) VALUES (NEW.workspace_id, 1) ON CONFLICT(workspace_id) DO UPDATE SET revision = revision + 1; END; CREATE TRIGGER exact_task_workspace_fence_update AFTER UPDATE ON tasks BEGIN INSERT INTO exact_task_workspace_fences(workspace_id, revision) VALUES (NEW.workspace_id, 1) ON CONFLICT(workspace_id) DO UPDATE SET revision = revision + 1; INSERT INTO exact_task_workspace_fences(workspace_id, revision) SELECT OLD.workspace_id, 1 WHERE OLD.workspace_id <> NEW.workspace_id ON CONFLICT(workspace_id) DO UPDATE SET revision = revision + 1; END; CREATE TRIGGER exact_task_workspace_fence_delete AFTER DELETE ON tasks BEGIN INSERT INTO exact_task_workspace_fences(workspace_id, revision) VALUES (OLD.workspace_id, 1) ON CONFLICT(workspace_id) DO UPDATE SET revision = revision + 1; END`)
}

func (r *Repository) OpenExactTaskSnapshot(ctx context.Context, request models.ExactTaskSnapshotRequest) (*models.ExactTaskSnapshot, error) {
	if request.WorkspaceID == "" {
		return nil, fmt.Errorf("exact task snapshot workspace is required")
	}
	ttl := request.TTL
	if ttl == 0 {
		ttl = exactTaskSnapshotDefaultTTL
	}
	if ttl < 0 || ttl > exactTaskSnapshotMaxTTL {
		return nil, fmt.Errorf("exact task snapshot TTL is invalid")
	}
	expiresAt := r.nowUTC().Add(ttl)
	token := uuid.NewString()
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, r.db.Rebind(`INSERT INTO exact_task_workspace_fences(workspace_id, revision) VALUES (?, 0) ON CONFLICT(workspace_id) DO NOTHING`), request.WorkspaceID); err != nil {
		return nil, err
	}
	var revision int64
	if err = tx.QueryRowxContext(ctx, r.db.Rebind(`SELECT revision FROM exact_task_workspace_fences WHERE workspace_id = ?`)+r.exactTaskFenceLockClause(true), request.WorkspaceID).Scan(&revision); err != nil {
		return nil, err
	}
	projection, err := r.materializeExactTaskSnapshotTasks(ctx, tx, request)
	if err != nil {
		return nil, err
	}
	if len(projection) > exactTaskSnapshotMaxRows {
		return nil, fmt.Errorf("exact task snapshot exceeds %d tasks", exactTaskSnapshotMaxRows)
	}
	if _, err = tx.ExecContext(ctx, r.db.Rebind(`INSERT INTO exact_task_snapshots(token, workspace_id, include_archived, include_ephemeral, workspace_revision, expires_at) VALUES (?, ?, ?, ?, ?, ?)`), token, request.WorkspaceID, boolInt(request.IncludeArchived), boolInt(request.IncludeEphemeral), revision, expiresAt); err != nil {
		return nil, err
	}
	for ordinal, task := range projection {
		if _, err = tx.ExecContext(ctx, r.db.Rebind(`INSERT INTO exact_task_snapshot_rows(snapshot_token, ordinal, task_id, workspace_id, workflow_id, workflow_step_id, title, description, state, priority, position, archived, resource_version) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`), token, ordinal, task.ID, task.WorkspaceID, task.WorkflowID, task.WorkflowStepID, task.Title, task.Description, task.State, task.Priority, task.Position, boolInt(task.Archived), task.ResourceVersion); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &models.ExactTaskSnapshot{Token: token, WorkspaceID: request.WorkspaceID, ExpiresAt: expiresAt}, nil
}

func (r *Repository) materializeExactTaskSnapshotTasks(ctx context.Context, tx *sqlx.Tx, request models.ExactTaskSnapshotRequest) ([]models.ExactTaskSnapshotTask, error) {
	rows, err := tx.QueryxContext(ctx, r.db.Rebind(`SELECT id, workspace_id, workflow_id, workflow_step_id, title, description, state, priority, position, CASE WHEN archived_at IS NULL THEN 0 ELSE 1 END, resource_version FROM tasks WHERE workspace_id = ? AND (? = 1 OR archived_at IS NULL) AND (? = 1 OR is_ephemeral = 0) ORDER BY workflow_id, workflow_step_id, position, id LIMIT ?`), request.WorkspaceID, boolInt(request.IncludeArchived), boolInt(request.IncludeEphemeral), exactTaskSnapshotMaxRows+1)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	projection := make([]models.ExactTaskSnapshotTask, 0)
	for rows.Next() {
		var task models.ExactTaskSnapshotTask
		if err := rows.Scan(&task.ID, &task.WorkspaceID, &task.WorkflowID, &task.WorkflowStepID, &task.Title, &task.Description, &task.State, &task.Priority, &task.Position, &task.Archived, &task.ResourceVersion); err != nil {
			return nil, err
		}
		projection = append(projection, task)
	}
	return projection, rows.Err()
}

func (r *Repository) PageExactTaskSnapshot(ctx context.Context, token string, offset, limit int) ([]models.ExactTaskSnapshotTask, error) {
	if offset < 0 || limit < 1 || limit > exactTaskSnapshotMaxPage {
		return nil, fmt.Errorf("exact task snapshot page is invalid")
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := r.requireExactTaskSnapshotTx(ctx, tx, token); err != nil {
		return nil, err
	}
	result, err := r.readExactTaskSnapshotRowsTx(ctx, tx, `WHERE snapshot_token = ? AND ordinal >= ? ORDER BY ordinal LIMIT ?`, token, offset, limit)
	if err != nil {
		return nil, err
	}
	return result, tx.Commit()
}

func (r *Repository) GetExactTaskSnapshotTask(ctx context.Context, token, taskID string) (*models.ExactTaskSnapshotTask, error) {
	if taskID == "" {
		return nil, fmt.Errorf("exact task snapshot task id is required")
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := r.requireExactTaskSnapshotTx(ctx, tx, token); err != nil {
		return nil, err
	}
	rows, err := r.readExactTaskSnapshotRowsTx(ctx, tx, `WHERE snapshot_token = ? AND task_id = ?`, token, taskID)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, repoerrors.ErrTaskNotFound
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &rows[0], nil
}

func (r *Repository) requireExactTaskSnapshotTx(ctx context.Context, tx *sqlx.Tx, token string) error {
	if token == "" {
		return repoerrors.ErrExactTaskSnapshotUnavailable
	}
	var workspaceID string
	var revision int64
	var expiresAt time.Time
	err := tx.QueryRowxContext(ctx, r.db.Rebind(`SELECT workspace_id, workspace_revision, expires_at FROM exact_task_snapshots WHERE token = ?`), token).Scan(&workspaceID, &revision, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) || expiresAt.Before(r.nowUTC()) {
		return repoerrors.ErrExactTaskSnapshotUnavailable
	}
	if err != nil {
		return err
	}
	var current int64
	if err = tx.QueryRowxContext(ctx, r.db.Rebind(`SELECT revision FROM exact_task_workspace_fences WHERE workspace_id = ?`)+r.exactTaskFenceLockClause(false), workspaceID).Scan(&current); err != nil || current != revision {
		return repoerrors.ErrExactTaskSnapshotUnavailable
	}
	return nil
}

func (r *Repository) readExactTaskSnapshotRowsTx(ctx context.Context, tx *sqlx.Tx, suffix string, args ...any) ([]models.ExactTaskSnapshotTask, error) {
	rows, err := tx.QueryxContext(ctx, r.db.Rebind(`SELECT task_id, workspace_id, workflow_id, workflow_step_id, title, description, state, priority, position, archived, resource_version FROM exact_task_snapshot_rows `+suffix), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := make([]models.ExactTaskSnapshotTask, 0)
	for rows.Next() {
		var task models.ExactTaskSnapshotTask
		if err := rows.Scan(&task.ID, &task.WorkspaceID, &task.WorkflowID, &task.WorkflowStepID, &task.Title, &task.Description, &task.State, &task.Priority, &task.Position, &task.Archived, &task.ResourceVersion); err != nil {
			return nil, err
		}
		result = append(result, task)
	}
	return result, rows.Err()
}

func (r *Repository) exactTaskFenceLockClause(exclusive bool) string {
	if !dialect.IsPostgres(r.db.DriverName()) {
		return ""
	}
	if exclusive {
		return forUpdateClause
	}
	return " FOR SHARE"
}

func (r *Repository) CleanupExpiredExactTaskSnapshots(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > exactTaskSnapshotCleanupMax {
		return 0, fmt.Errorf("exact task snapshot cleanup limit is invalid")
	}
	rows, err := r.db.QueryxContext(ctx, r.db.Rebind(`SELECT token FROM exact_task_snapshots WHERE expires_at <= ? ORDER BY expires_at, token LIMIT ?`), r.nowUTC(), limit)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	tokens := make([]string, 0, limit)
	for rows.Next() {
		var token string
		if err := rows.Scan(&token); err != nil {
			return 0, err
		}
		tokens = append(tokens, token)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(tokens) == 0 {
		return 0, nil
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(tokens)), ",")
	args := make([]any, len(tokens))
	for i := range tokens {
		args[i] = tokens[i]
	}
	result, err := r.db.ExecContext(ctx, r.db.Rebind(`DELETE FROM exact_task_snapshots WHERE token IN (`+placeholders+`)`), args...)
	if err != nil {
		return 0, err
	}
	count, err := result.RowsAffected()
	return int(count), err
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
