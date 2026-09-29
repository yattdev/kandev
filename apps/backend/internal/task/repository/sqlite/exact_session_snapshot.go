package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/kandev/kandev/internal/db/dialect"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository/repoerrors"
)

// ensureExactSessionResourceVersion runs after the legacy session-table
// normalization, which can rebuild task_sessions and discard additive columns
// and triggers installed before it.
func (r *Repository) ensureExactSessionResourceVersion() error {
	if err := r.migrate.Apply("task_sessions.resource_version", `ALTER TABLE task_sessions ADD COLUMN resource_version INTEGER NOT NULL DEFAULT 1`); err != nil {
		return err
	}
	if dialect.IsPostgres(r.db.DriverName()) {
		if err := r.migrate.Apply("task_sessions.resource_version_function", `CREATE OR REPLACE FUNCTION kandev_task_session_resource_version() RETURNS trigger AS $$ BEGIN NEW.resource_version := OLD.resource_version + 1; RETURN NEW; END; $$ LANGUAGE plpgsql`); err != nil {
			return err
		}
		return r.migrate.Apply("task_sessions.resource_version_trigger", `CREATE TRIGGER task_sessions_resource_version_trigger BEFORE UPDATE ON task_sessions FOR EACH ROW WHEN (NEW.resource_version = OLD.resource_version) EXECUTE FUNCTION kandev_task_session_resource_version()`)
	}
	return r.migrate.Apply("task_sessions.resource_version_trigger", `CREATE TRIGGER task_sessions_resource_version_trigger AFTER UPDATE ON task_sessions WHEN NEW.resource_version = OLD.resource_version BEGIN UPDATE task_sessions SET resource_version = OLD.resource_version + 1 WHERE id = OLD.id; END`)
}

func (r *Repository) initExactSessionSnapshotSchema() error {
	driver := r.db.DriverName()
	if err := r.migrate.Apply("exact_session_snapshots.tables", `
		CREATE TABLE IF NOT EXISTS exact_session_task_workspaces (
			task_id TEXT PRIMARY KEY,
			workspace_id TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS exact_session_workspace_fences (
			workspace_id TEXT PRIMARY KEY,
			revision BIGINT NOT NULL DEFAULT 0
		);
		CREATE TABLE IF NOT EXISTS exact_session_snapshots (
			token TEXT PRIMARY KEY,
			workspace_id TEXT NOT NULL,
			workspace_revision BIGINT NOT NULL,
			expires_at TIMESTAMP NOT NULL
		);
		CREATE TABLE IF NOT EXISTS exact_session_snapshot_rows (
			snapshot_token TEXT NOT NULL,
			ordinal INTEGER NOT NULL,
			session_id TEXT NOT NULL,
			task_id TEXT NOT NULL,
			workspace_id TEXT NOT NULL,
			queue_incarnation_id TEXT NOT NULL,
			state TEXT NOT NULL,
			route_generation BIGINT NOT NULL,
			started_at TIMESTAMP NOT NULL,
			completed_at TIMESTAMP,
			updated_at TIMESTAMP NOT NULL,
			is_primary INTEGER NOT NULL,
			resource_version BIGINT NOT NULL,
			PRIMARY KEY (snapshot_token, ordinal),
			UNIQUE (snapshot_token, session_id),
			FOREIGN KEY (snapshot_token) REFERENCES exact_session_snapshots(token) ON DELETE CASCADE
		);
		CREATE INDEX IF NOT EXISTS idx_exact_session_snapshots_expiry ON exact_session_snapshots(expires_at);
	`); err != nil {
		return fmt.Errorf("create exact session snapshot tables: %w", err)
	}
	if dialect.IsPostgres(driver) {
		if _, err := r.db.Exec(`INSERT INTO exact_session_task_workspaces(task_id, workspace_id) SELECT id, workspace_id FROM tasks ON CONFLICT(task_id) DO UPDATE SET workspace_id = EXCLUDED.workspace_id`); err != nil {
			return fmt.Errorf("backfill exact session task workspaces: %w", err)
		}
	} else if _, err := r.db.Exec(`INSERT OR REPLACE INTO exact_session_task_workspaces(task_id, workspace_id) SELECT id, workspace_id FROM tasks`); err != nil {
		return fmt.Errorf("backfill exact session task workspaces: %w", err)
	}
	if dialect.IsPostgres(driver) {
		if err := r.migrate.Apply("exact_session_snapshots.task_fence_function", `CREATE OR REPLACE FUNCTION kandev_exact_session_task_workspace_fence() RETURNS trigger AS $$ BEGIN IF TG_OP = 'DELETE' THEN DELETE FROM exact_session_task_workspaces WHERE task_id = OLD.id; INSERT INTO exact_session_workspace_fences(workspace_id, revision) VALUES (OLD.workspace_id, 1) ON CONFLICT(workspace_id) DO UPDATE SET revision = exact_session_workspace_fences.revision + 1; ELSE INSERT INTO exact_session_task_workspaces(task_id, workspace_id) VALUES (NEW.id, NEW.workspace_id) ON CONFLICT(task_id) DO UPDATE SET workspace_id = EXCLUDED.workspace_id; INSERT INTO exact_session_workspace_fences(workspace_id, revision) VALUES (NEW.workspace_id, 1) ON CONFLICT(workspace_id) DO UPDATE SET revision = exact_session_workspace_fences.revision + 1; IF TG_OP = 'UPDATE' AND NEW.workspace_id <> OLD.workspace_id THEN INSERT INTO exact_session_workspace_fences(workspace_id, revision) VALUES (OLD.workspace_id, 1) ON CONFLICT(workspace_id) DO UPDATE SET revision = exact_session_workspace_fences.revision + 1; END IF; END IF; RETURN NULL; END; $$ LANGUAGE plpgsql`); err != nil {
			return fmt.Errorf("create exact session task fence function: %w", err)
		}
		if err := r.migrate.Apply("exact_session_snapshots.session_fence_function", `CREATE OR REPLACE FUNCTION kandev_exact_session_workspace_fence() RETURNS trigger AS $$ BEGIN INSERT INTO exact_session_workspace_fences(workspace_id, revision) SELECT workspace_id, 1 FROM exact_session_task_workspaces WHERE task_id = CASE WHEN TG_OP = 'DELETE' THEN OLD.task_id ELSE NEW.task_id END ON CONFLICT(workspace_id) DO UPDATE SET revision = exact_session_workspace_fences.revision + 1; IF TG_OP = 'UPDATE' AND NEW.task_id <> OLD.task_id THEN INSERT INTO exact_session_workspace_fences(workspace_id, revision) SELECT workspace_id, 1 FROM exact_session_task_workspaces WHERE task_id = OLD.task_id ON CONFLICT(workspace_id) DO UPDATE SET revision = exact_session_workspace_fences.revision + 1; END IF; RETURN NULL; END; $$ LANGUAGE plpgsql`); err != nil {
			return fmt.Errorf("create exact session fence function: %w", err)
		}
		if err := r.migrate.Apply("exact_session_snapshots.task_fence_trigger", `CREATE TRIGGER exact_session_task_workspace_fence_trigger AFTER INSERT OR UPDATE OR DELETE ON tasks FOR EACH ROW EXECUTE FUNCTION kandev_exact_session_task_workspace_fence()`); err != nil {
			return err
		}
		return r.migrate.Apply("exact_session_snapshots.session_fence_trigger", `CREATE TRIGGER exact_session_workspace_fence_trigger AFTER INSERT OR UPDATE OR DELETE ON task_sessions FOR EACH ROW EXECUTE FUNCTION kandev_exact_session_workspace_fence()`)
	}
	return r.migrate.Apply("exact_session_snapshots.fence_triggers", `
		CREATE TRIGGER exact_session_task_workspace_fence_insert AFTER INSERT ON tasks BEGIN INSERT INTO exact_session_task_workspaces(task_id, workspace_id) VALUES (NEW.id, NEW.workspace_id) ON CONFLICT(task_id) DO UPDATE SET workspace_id = excluded.workspace_id; INSERT INTO exact_session_workspace_fences(workspace_id, revision) VALUES (NEW.workspace_id, 1) ON CONFLICT(workspace_id) DO UPDATE SET revision = revision + 1; END;
		CREATE TRIGGER exact_session_task_workspace_fence_update AFTER UPDATE ON tasks BEGIN INSERT INTO exact_session_task_workspaces(task_id, workspace_id) VALUES (NEW.id, NEW.workspace_id) ON CONFLICT(task_id) DO UPDATE SET workspace_id = excluded.workspace_id; INSERT INTO exact_session_workspace_fences(workspace_id, revision) VALUES (NEW.workspace_id, 1) ON CONFLICT(workspace_id) DO UPDATE SET revision = revision + 1; INSERT INTO exact_session_workspace_fences(workspace_id, revision) SELECT OLD.workspace_id, 1 WHERE OLD.workspace_id <> NEW.workspace_id ON CONFLICT(workspace_id) DO UPDATE SET revision = revision + 1; END;
		CREATE TRIGGER exact_session_task_workspace_fence_delete AFTER DELETE ON tasks BEGIN DELETE FROM exact_session_task_workspaces WHERE task_id = OLD.id; INSERT INTO exact_session_workspace_fences(workspace_id, revision) VALUES (OLD.workspace_id, 1) ON CONFLICT(workspace_id) DO UPDATE SET revision = revision + 1; END;
		CREATE TRIGGER exact_session_workspace_fence_insert AFTER INSERT ON task_sessions BEGIN INSERT INTO exact_session_workspace_fences(workspace_id, revision) SELECT workspace_id, 1 FROM exact_session_task_workspaces WHERE task_id = NEW.task_id ON CONFLICT(workspace_id) DO UPDATE SET revision = revision + 1; END;
		CREATE TRIGGER exact_session_workspace_fence_update AFTER UPDATE ON task_sessions WHEN NEW.resource_version <> OLD.resource_version BEGIN INSERT INTO exact_session_workspace_fences(workspace_id, revision) SELECT workspace_id, 1 FROM exact_session_task_workspaces WHERE task_id = NEW.task_id ON CONFLICT(workspace_id) DO UPDATE SET revision = revision + 1; INSERT INTO exact_session_workspace_fences(workspace_id, revision) SELECT workspace_id, 1 FROM exact_session_task_workspaces WHERE task_id = OLD.task_id AND OLD.task_id <> NEW.task_id ON CONFLICT(workspace_id) DO UPDATE SET revision = revision + 1; END;
		CREATE TRIGGER exact_session_workspace_fence_delete AFTER DELETE ON task_sessions BEGIN INSERT INTO exact_session_workspace_fences(workspace_id, revision) SELECT workspace_id, 1 FROM exact_session_task_workspaces WHERE task_id = OLD.task_id ON CONFLICT(workspace_id) DO UPDATE SET revision = revision + 1; END;
	`)
}

func (r *Repository) OpenExactSessionSnapshot(ctx context.Context, request models.ExactSessionSnapshotRequest) (*models.ExactSessionSnapshot, error) {
	if request.WorkspaceID == "" {
		return nil, fmt.Errorf("exact session snapshot workspace is required")
	}
	ttl := request.TTL
	if ttl == 0 {
		ttl = exactTaskSnapshotDefaultTTL
	}
	if ttl < 0 || ttl > exactTaskSnapshotMaxTTL {
		return nil, fmt.Errorf("exact session snapshot TTL is invalid")
	}
	if _, err := r.CleanupExpiredExactSessionSnapshots(ctx, exactTaskSnapshotCleanupMax); err != nil {
		return nil, fmt.Errorf("cleanup expired exact session snapshots: %w", err)
	}
	expiresAt := r.nowUTC().Add(ttl)
	token := uuid.NewString()
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, r.db.Rebind(`INSERT INTO exact_session_workspace_fences(workspace_id, revision) VALUES (?, 0) ON CONFLICT(workspace_id) DO NOTHING`), request.WorkspaceID); err != nil {
		return nil, err
	}
	var revision int64
	if err = tx.QueryRowxContext(ctx, r.db.Rebind(`SELECT revision FROM exact_session_workspace_fences WHERE workspace_id = ?`)+r.exactSessionFenceLockClause(true), request.WorkspaceID).Scan(&revision); err != nil {
		return nil, err
	}
	projection, err := r.materializeExactSessionSnapshotRows(ctx, tx, request.WorkspaceID)
	if err != nil {
		return nil, err
	}
	if len(projection) > exactTaskSnapshotMaxRows {
		return nil, fmt.Errorf("exact session snapshot exceeds %d sessions", exactTaskSnapshotMaxRows)
	}
	if _, err = tx.ExecContext(ctx, r.db.Rebind(`INSERT INTO exact_session_snapshots(token, workspace_id, workspace_revision, expires_at) VALUES (?, ?, ?, ?)`), token, request.WorkspaceID, revision, expiresAt); err != nil {
		return nil, err
	}
	for ordinal, session := range projection {
		if _, err = tx.ExecContext(ctx, r.db.Rebind(`INSERT INTO exact_session_snapshot_rows(snapshot_token, ordinal, session_id, task_id, workspace_id, queue_incarnation_id, state, route_generation, started_at, completed_at, updated_at, is_primary, resource_version) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`), token, ordinal, session.ID, session.TaskID, session.WorkspaceID, session.QueueIncarnationID, string(session.State), session.RouteGeneration, session.StartedAt, session.CompletedAt, session.UpdatedAt, boolInt(session.IsPrimary), session.ResourceVersion); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &models.ExactSessionSnapshot{Token: token, WorkspaceID: request.WorkspaceID, ExpiresAt: expiresAt}, nil
}

func (r *Repository) materializeExactSessionSnapshotRows(ctx context.Context, tx *sqlx.Tx, workspaceID string) ([]models.ExactSessionSnapshotSession, error) {
	rows, err := tx.QueryxContext(ctx, r.db.Rebind(`SELECT ts.id, ts.task_id, t.workspace_id, ts.queue_incarnation_id, ts.state, ts.route_generation, ts.started_at, ts.completed_at, ts.updated_at, ts.is_primary, ts.resource_version FROM task_sessions ts JOIN tasks t ON t.id = ts.task_id WHERE t.workspace_id = ? ORDER BY ts.started_at, ts.id LIMIT ?`), workspaceID, exactTaskSnapshotMaxRows+1)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	projection := make([]models.ExactSessionSnapshotSession, 0)
	for rows.Next() {
		var session models.ExactSessionSnapshotSession
		var completedAt sql.NullTime
		var primary int
		if err := rows.Scan(&session.ID, &session.TaskID, &session.WorkspaceID, &session.QueueIncarnationID, &session.State, &session.RouteGeneration, &session.StartedAt, &completedAt, &session.UpdatedAt, &primary, &session.ResourceVersion); err != nil {
			return nil, err
		}
		if completedAt.Valid {
			session.CompletedAt = &completedAt.Time
		}
		session.IsPrimary = primary == 1
		projection = append(projection, session)
	}
	return projection, rows.Err()
}

func (r *Repository) PageExactSessionSnapshot(ctx context.Context, token string, offset, limit int) ([]models.ExactSessionSnapshotSession, error) {
	if offset < 0 || limit < 1 || limit > exactTaskSnapshotMaxPage {
		return nil, fmt.Errorf("exact session snapshot page is invalid")
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := r.requireExactSessionSnapshotTx(ctx, tx, token); err != nil {
		return nil, err
	}
	if r.exactSessionSnapshotReadAfterFenceHook != nil {
		r.exactSessionSnapshotReadAfterFenceHook()
	}
	items, err := r.readExactSessionSnapshotRowsTx(ctx, tx, `WHERE snapshot_token = ? AND ordinal >= ? ORDER BY ordinal LIMIT ?`, token, offset, limit)
	if err != nil {
		return nil, err
	}
	return items, tx.Commit()
}

func (r *Repository) GetExactSessionSnapshotSession(ctx context.Context, token, sessionID string) (*models.ExactSessionSnapshotSession, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("exact session snapshot session id is required")
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := r.requireExactSessionSnapshotTx(ctx, tx, token); err != nil {
		return nil, err
	}
	if r.exactSessionSnapshotReadAfterFenceHook != nil {
		r.exactSessionSnapshotReadAfterFenceHook()
	}
	items, err := r.readExactSessionSnapshotRowsTx(ctx, tx, `WHERE snapshot_token = ? AND session_id = ?`, token, sessionID)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, nil
	}
	return &items[0], tx.Commit()
}

func (r *Repository) requireExactSessionSnapshotTx(ctx context.Context, tx *sqlx.Tx, token string) error {
	return r.requireExactWorkspaceSnapshotTx(ctx, tx, token, "exact_session_snapshots", "exact_session_workspace_fences", r.exactSessionFenceLockClause(false), repoerrors.ErrExactSessionSnapshotUnavailable)
}

func (r *Repository) readExactSessionSnapshotRowsTx(ctx context.Context, tx *sqlx.Tx, suffix string, args ...any) ([]models.ExactSessionSnapshotSession, error) {
	rows, err := tx.QueryxContext(ctx, r.db.Rebind(`SELECT session_id, task_id, workspace_id, queue_incarnation_id, state, route_generation, started_at, completed_at, updated_at, is_primary, resource_version FROM exact_session_snapshot_rows `+suffix), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := make([]models.ExactSessionSnapshotSession, 0)
	for rows.Next() {
		var session models.ExactSessionSnapshotSession
		var completedAt sql.NullTime
		var primary int
		if err := rows.Scan(&session.ID, &session.TaskID, &session.WorkspaceID, &session.QueueIncarnationID, &session.State, &session.RouteGeneration, &session.StartedAt, &completedAt, &session.UpdatedAt, &primary, &session.ResourceVersion); err != nil {
			return nil, err
		}
		if completedAt.Valid {
			session.CompletedAt = &completedAt.Time
		}
		session.IsPrimary = primary == 1
		result = append(result, session)
	}
	return result, rows.Err()
}

func (r *Repository) exactSessionFenceLockClause(write bool) string {
	if !dialect.IsPostgres(r.db.DriverName()) {
		return ""
	}
	if write {
		return forUpdateClause
	}
	return " FOR SHARE"
}

func (r *Repository) CleanupExpiredExactSessionSnapshots(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > exactTaskSnapshotCleanupMax {
		return 0, fmt.Errorf("exact session snapshot cleanup limit is invalid")
	}
	rows, err := r.db.QueryxContext(ctx, r.db.Rebind(`SELECT token FROM exact_session_snapshots WHERE expires_at <= ? ORDER BY expires_at, token LIMIT ?`), r.nowUTC(), limit)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	tokens := make([]string, 0)
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
	placeholders := make([]string, len(tokens))
	args := make([]any, len(tokens))
	for i, token := range tokens {
		placeholders[i] = "?"
		args[i] = token
	}
	result, err := r.db.ExecContext(ctx, r.db.Rebind(`DELETE FROM exact_session_snapshots WHERE token IN (`+joinExactSessionPlaceholders(placeholders)+`)`), args...)
	if err != nil {
		return 0, err
	}
	count, err := result.RowsAffected()
	return int(count), err
}

func joinExactSessionPlaceholders(values []string) string {
	if len(values) == 0 {
		return ""
	}
	result := values[0]
	for _, value := range values[1:] {
		result += "," + value
	}
	return result
}
