package messagequeue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	internaldb "github.com/kandev/kandev/internal/db"
	"github.com/kandev/kandev/internal/exactsnapshotauthority"
)

const exactPendingTTL = 5 * time.Minute
const exactPendingMaxTTL = 15 * time.Minute
const exactPendingMaxRows = 500
const exactPendingMaxPage = 100
const exactPendingCleanupMax = 100

var ErrExactPendingTransitionUnavailable = errors.New("exact pending transition unavailable")
var ErrExactPendingTransitionBoundary = errors.New("exact pending transition boundary invalid")

func (r *sqliteRepository) initExactPendingTransitionSchema() error {
	if r.db.DriverName() == postgresDriverName || !r.tasksTablePresent || !r.taskSessionsTablePresent {
		return nil
	}
	required := [][2]string{{"tasks", "workspace_id"}, {"tasks", "resource_version"}, {"task_sessions", "task_id"}, {"task_sessions", "queue_incarnation_id"}, {"task_sessions", "resource_version"}}
	for _, column := range required {
		ok, err := internaldb.ColumnExists(r.db, column[0], column[1])
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
	}
	if _, err := r.db.Exec(`CREATE TABLE IF NOT EXISTS exact_pending_task_workspaces(task_id TEXT PRIMARY KEY,workspace_id TEXT NOT NULL,resource_version BIGINT NOT NULL); CREATE TABLE IF NOT EXISTS exact_pending_session_versions(session_id TEXT PRIMARY KEY,task_id TEXT NOT NULL,incarnation_id TEXT NOT NULL,resource_version BIGINT NOT NULL); CREATE TABLE IF NOT EXISTS exact_pending_fences(workspace_id TEXT PRIMARY KEY,revision BIGINT NOT NULL DEFAULT 0); CREATE TABLE IF NOT EXISTS exact_pending_snapshots(token TEXT PRIMARY KEY,workspace_id TEXT NOT NULL,workspace_revision BIGINT NOT NULL,expires_at TIMESTAMP NOT NULL); CREATE TABLE IF NOT EXISTS exact_pending_snapshot_rows(snapshot_token TEXT NOT NULL,ordinal INTEGER NOT NULL,session_id TEXT NOT NULL,task_id TEXT NOT NULL,workspace_id TEXT NOT NULL,session_incarnation_id TEXT NOT NULL,workflow_id TEXT NOT NULL,workflow_step_id TEXT NOT NULL,step_position INTEGER NOT NULL,queued_at TIMESTAMP NOT NULL,resource_version BIGINT NOT NULL,task_resource_version BIGINT NOT NULL,session_resource_version BIGINT NOT NULL,queue_generation BIGINT NOT NULL,PRIMARY KEY(snapshot_token,ordinal),UNIQUE(snapshot_token,session_id),FOREIGN KEY(snapshot_token) REFERENCES exact_pending_snapshots(token) ON DELETE CASCADE); CREATE INDEX IF NOT EXISTS idx_exact_pending_snapshots_expiry ON exact_pending_snapshots(expires_at)`); err != nil {
		return err
	}
	if _, err := r.db.Exec(`INSERT OR REPLACE INTO exact_pending_task_workspaces SELECT id,workspace_id,resource_version FROM tasks; INSERT OR REPLACE INTO exact_pending_session_versions SELECT id,task_id,queue_incarnation_id,resource_version FROM task_sessions`); err != nil {
		return err
	}
	_, err := r.db.Exec(`CREATE TRIGGER IF NOT EXISTS exact_pending_task_insert AFTER INSERT ON tasks BEGIN INSERT INTO exact_pending_task_workspaces VALUES(NEW.id,NEW.workspace_id,NEW.resource_version) ON CONFLICT(task_id) DO UPDATE SET workspace_id=excluded.workspace_id,resource_version=excluded.resource_version; INSERT INTO exact_pending_fences VALUES(NEW.workspace_id,1) ON CONFLICT(workspace_id) DO UPDATE SET revision=revision+1; END; CREATE TRIGGER IF NOT EXISTS exact_pending_task_update AFTER UPDATE ON tasks BEGIN INSERT INTO exact_pending_task_workspaces VALUES(NEW.id,NEW.workspace_id,NEW.resource_version) ON CONFLICT(task_id) DO UPDATE SET workspace_id=excluded.workspace_id,resource_version=excluded.resource_version; INSERT INTO exact_pending_fences VALUES(NEW.workspace_id,1) ON CONFLICT(workspace_id) DO UPDATE SET revision=revision+1; END; CREATE TRIGGER IF NOT EXISTS exact_pending_task_delete AFTER DELETE ON tasks BEGIN DELETE FROM exact_pending_task_workspaces WHERE task_id=OLD.id; INSERT INTO exact_pending_fences VALUES(OLD.workspace_id,1) ON CONFLICT(workspace_id) DO UPDATE SET revision=revision+1; END; CREATE TRIGGER IF NOT EXISTS exact_pending_session_insert AFTER INSERT ON task_sessions BEGIN INSERT INTO exact_pending_session_versions VALUES(NEW.id,NEW.task_id,NEW.queue_incarnation_id,NEW.resource_version) ON CONFLICT(session_id) DO UPDATE SET task_id=excluded.task_id,incarnation_id=excluded.incarnation_id,resource_version=excluded.resource_version; INSERT INTO exact_pending_fences SELECT workspace_id,1 FROM exact_pending_task_workspaces WHERE task_id=NEW.task_id ON CONFLICT(workspace_id) DO UPDATE SET revision=revision+1; END; CREATE TRIGGER IF NOT EXISTS exact_pending_session_update AFTER UPDATE ON task_sessions BEGIN INSERT INTO exact_pending_session_versions VALUES(NEW.id,NEW.task_id,NEW.queue_incarnation_id,NEW.resource_version) ON CONFLICT(session_id) DO UPDATE SET task_id=excluded.task_id,incarnation_id=excluded.incarnation_id,resource_version=excluded.resource_version; INSERT INTO exact_pending_fences SELECT workspace_id,1 FROM exact_pending_task_workspaces WHERE task_id=NEW.task_id ON CONFLICT(workspace_id) DO UPDATE SET revision=revision+1; END; CREATE TRIGGER IF NOT EXISTS exact_pending_session_delete AFTER DELETE ON task_sessions BEGIN DELETE FROM exact_pending_session_versions WHERE session_id=OLD.id; INSERT INTO exact_pending_fences SELECT workspace_id,1 FROM exact_pending_task_workspaces WHERE task_id=OLD.task_id ON CONFLICT(workspace_id) DO UPDATE SET revision=revision+1; END; CREATE TRIGGER IF NOT EXISTS exact_pending_move_version AFTER UPDATE ON pending_moves WHEN NEW.resource_version=OLD.resource_version BEGIN UPDATE pending_moves SET resource_version=OLD.resource_version+1 WHERE session_id=OLD.session_id; END; CREATE TRIGGER IF NOT EXISTS exact_pending_move_insert AFTER INSERT ON pending_moves BEGIN INSERT INTO exact_pending_fences SELECT workspace_id,1 FROM exact_pending_task_workspaces WHERE task_id=NEW.task_id ON CONFLICT(workspace_id) DO UPDATE SET revision=revision+1; END; CREATE TRIGGER IF NOT EXISTS exact_pending_move_update AFTER UPDATE ON pending_moves WHEN NEW.resource_version<>OLD.resource_version BEGIN INSERT INTO exact_pending_fences SELECT workspace_id,1 FROM exact_pending_task_workspaces WHERE task_id=NEW.task_id ON CONFLICT(workspace_id) DO UPDATE SET revision=revision+1; END; CREATE TRIGGER IF NOT EXISTS exact_pending_move_delete AFTER DELETE ON pending_moves BEGIN INSERT INTO exact_pending_fences SELECT workspace_id,1 FROM exact_pending_task_workspaces WHERE task_id=OLD.task_id ON CONFLICT(workspace_id) DO UPDATE SET revision=revision+1; END`)
	if err == nil {
		r.exactPendingTransitionsEnabled = true
	}
	return err
}

func (r *sqliteRepository) OpenExactPendingTransitionSnapshot(ctx context.Context, req ExactPendingTransitionSnapshotRequest) (*ExactPendingTransitionSnapshot, error) {
	if !r.exactPendingTransitionsEnabled || r.db.DriverName() == postgresDriverName {
		return nil, ErrExactPendingTransitionUnavailable
	}
	if _, err := r.CleanupExpiredExactPendingTransitionSnapshots(ctx, exactPendingCleanupMax); err != nil {
		return nil, err
	}
	tx, err := r.BeginExactPendingTransitionSnapshotTx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	snapshot, err := r.OpenExactPendingTransitionSnapshotInTx(ctx, tx, req)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return snapshot, nil
}

func (r *sqliteRepository) BeginExactPendingTransitionSnapshotTx(ctx context.Context) (*sqlx.Tx, error) {
	if !r.exactPendingTransitionsEnabled || r.db.DriverName() == postgresDriverName {
		return nil, ErrExactPendingTransitionUnavailable
	}
	return r.db.BeginTxx(ctx, nil)
}

// ValidateExactPendingTransitionSnapshotAuthority verifies the authority
// before a composite transaction is opened.
func (r *sqliteRepository) ValidateExactPendingTransitionSnapshotAuthority(authority *exactsnapshotauthority.Authority) error {
	if !r.exactPendingTransitionsEnabled || r.db.DriverName() == postgresDriverName || !authority.Matches(r.db) {
		return ErrExactPendingTransitionUnavailable
	}
	return nil
}

func (r *sqliteRepository) BeginExactPendingTransitionSnapshotAuthorityTx(ctx context.Context, authority *exactsnapshotauthority.Authority) (*exactsnapshotauthority.Transaction, error) {
	if err := r.ValidateExactPendingTransitionSnapshotAuthority(authority); err != nil {
		return nil, err
	}
	tx, err := authority.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return tx, nil
}

// OpenExactPendingTransitionSnapshotInAuthorityTx only accepts a transaction
// issued by the matching shared SQLite authority.
func (r *sqliteRepository) OpenExactPendingTransitionSnapshotInAuthorityTx(ctx context.Context, authority *exactsnapshotauthority.Authority, tx *exactsnapshotauthority.Transaction, req ExactPendingTransitionSnapshotRequest) (*ExactPendingTransitionSnapshot, error) {
	if !r.exactPendingTransitionsEnabled || r.db.DriverName() == postgresDriverName || !authority.Matches(r.db) || !tx.Matches(authority) {
		return nil, ErrExactPendingTransitionUnavailable
	}
	return r.OpenExactPendingTransitionSnapshotInTx(ctx, tx.SQLX(), req)
}

func (r *sqliteRepository) OpenExactPendingTransitionSnapshotInTx(ctx context.Context, tx *sqlx.Tx, req ExactPendingTransitionSnapshotRequest) (*ExactPendingTransitionSnapshot, error) {
	if !r.exactPendingTransitionsEnabled || tx == nil || r.db.DriverName() == postgresDriverName {
		return nil, ErrExactPendingTransitionUnavailable
	}
	ttl, err := exactPendingSnapshotTTL(req)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, r.db.Rebind(`INSERT INTO exact_pending_fences VALUES(?,0) ON CONFLICT(workspace_id) DO NOTHING`), req.WorkspaceID); err != nil {
		return nil, err
	}
	var rev int64
	if err = tx.GetContext(ctx, &rev, r.db.Rebind(`SELECT revision FROM exact_pending_fences WHERE workspace_id=?`), req.WorkspaceID); err != nil {
		return nil, err
	}
	items, err := r.pendingRows(ctx, tx, req.WorkspaceID)
	if err != nil {
		return nil, err
	}
	if len(items) > exactPendingMaxRows {
		return nil, fmt.Errorf("exact pending snapshot exceeds %d rows", exactPendingMaxRows)
	}
	expiry := time.Now().UTC().Add(ttl)
	token := uuid.NewString()
	if _, err = tx.ExecContext(ctx, r.db.Rebind(`INSERT INTO exact_pending_snapshots VALUES(?,?,?,?)`), token, req.WorkspaceID, rev, expiry); err != nil {
		return nil, err
	}
	for i, x := range items {
		if _, err = tx.ExecContext(ctx, r.db.Rebind(`INSERT INTO exact_pending_snapshot_rows VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`), token, i, x.SessionID, x.TaskID, x.WorkspaceID, x.SessionIncarnationID, x.WorkflowID, x.WorkflowStepID, x.Position, x.QueuedAt, x.ResourceVersion, x.TaskResourceVersion, x.SessionResourceVersion, x.QueueGeneration); err != nil {
			return nil, err
		}
	}
	return &ExactPendingTransitionSnapshot{Token: token, WorkspaceID: req.WorkspaceID, ExpiresAt: expiry}, nil
}

func exactPendingSnapshotTTL(req ExactPendingTransitionSnapshotRequest) (time.Duration, error) {
	if req.WorkspaceID == "" {
		return 0, fmt.Errorf("exact pending workspace required")
	}
	ttl := req.TTL
	if ttl == 0 {
		ttl = exactPendingTTL
	}
	if ttl < 0 || ttl > exactPendingMaxTTL {
		return 0, fmt.Errorf("exact pending TTL invalid")
	}
	return ttl, nil
}

func (r *sqliteRepository) pendingRows(ctx context.Context, tx *sqlx.Tx, ws string) ([]ExactPendingTransition, error) {
	rows, err := tx.QueryxContext(ctx, r.db.Rebind(`SELECT p.session_id,p.task_id,t.workspace_id,p.session_incarnation_id,p.workflow_id,p.workflow_step_id,p.step_position,p.queued_at,p.resource_version,t.resource_version,s.resource_version,COALESCE(q.status_generation,0) FROM pending_moves p JOIN tasks t ON t.id=p.task_id JOIN task_sessions s ON s.id=p.session_id AND s.task_id=p.task_id AND s.queue_incarnation_id=p.session_incarnation_id LEFT JOIN queue_session_state q ON q.session_id=p.session_id WHERE t.workspace_id=? ORDER BY p.queued_at,p.session_id LIMIT ?`), ws, exactPendingMaxRows+1)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []ExactPendingTransition{}
	for rows.Next() {
		var x ExactPendingTransition
		if err = rows.Scan(&x.SessionID, &x.TaskID, &x.WorkspaceID, &x.SessionIncarnationID, &x.WorkflowID, &x.WorkflowStepID, &x.Position, &x.QueuedAt, &x.ResourceVersion, &x.TaskResourceVersion, &x.SessionResourceVersion, &x.QueueGeneration); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	var bad int
	if err = tx.GetContext(ctx, &bad, r.db.Rebind(`SELECT COUNT(*) FROM pending_moves p JOIN tasks t ON t.id=p.task_id WHERE t.workspace_id=? AND NOT EXISTS(SELECT 1 FROM task_sessions s WHERE s.id=p.session_id AND s.task_id=p.task_id AND s.queue_incarnation_id=p.session_incarnation_id)`), ws); err != nil {
		return nil, err
	}
	if bad > 0 {
		return nil, ErrExactPendingTransitionBoundary
	}
	return out, nil
}
func (r *sqliteRepository) PageExactPendingTransitionSnapshot(ctx context.Context, token string, offset, limit int) ([]ExactPendingTransition, error) {
	if offset < 0 || limit < 1 || limit > exactPendingMaxPage {
		return nil, fmt.Errorf("exact pending page invalid")
	}
	if !r.exactPendingTransitionsEnabled {
		return nil, ErrExactPendingTransitionUnavailable
	}
	if err := r.requireExactPending(ctx, token); err != nil {
		return nil, err
	}
	var out []ExactPendingTransition
	err := r.ro.SelectContext(ctx, &out, r.ro.Rebind(`SELECT session_id,task_id,workspace_id,session_incarnation_id,workflow_id,workflow_step_id,step_position,queued_at,resource_version,task_resource_version,session_resource_version,queue_generation FROM exact_pending_snapshot_rows WHERE snapshot_token=? AND ordinal>=? ORDER BY ordinal LIMIT ?`), token, offset, limit)
	return out, err
}
func (r *sqliteRepository) GetExactPendingTransition(ctx context.Context, token, sessionID string) (*ExactPendingTransition, error) {
	if !r.exactPendingTransitionsEnabled {
		return nil, ErrExactPendingTransitionUnavailable
	}
	if err := r.requireExactPending(ctx, token); err != nil {
		return nil, err
	}
	var x ExactPendingTransition
	err := r.ro.GetContext(ctx, &x, r.ro.Rebind(`SELECT session_id,task_id,workspace_id,session_incarnation_id,workflow_id,workflow_step_id,step_position,queued_at,resource_version,task_resource_version,session_resource_version,queue_generation FROM exact_pending_snapshot_rows WHERE snapshot_token=? AND session_id=?`), token, sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &x, err
}
func (r *sqliteRepository) requireExactPending(ctx context.Context, token string) error {
	var exp time.Time
	var a, b int64
	err := r.ro.QueryRowxContext(ctx, r.ro.Rebind(`SELECT s.expires_at,s.workspace_revision,f.revision FROM exact_pending_snapshots s JOIN exact_pending_fences f ON f.workspace_id=s.workspace_id WHERE s.token=?`), token).Scan(&exp, &a, &b)
	if err != nil || !exp.After(time.Now().UTC()) || a != b {
		return ErrExactPendingTransitionUnavailable
	}
	return nil
}
func (r *sqliteRepository) CleanupExpiredExactPendingTransitionSnapshots(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > exactPendingCleanupMax {
		return 0, fmt.Errorf("exact pending cleanup limit invalid")
	}
	x, err := r.db.ExecContext(ctx, r.db.Rebind(`DELETE FROM exact_pending_snapshots WHERE token IN (SELECT token FROM exact_pending_snapshots WHERE expires_at<=? ORDER BY expires_at LIMIT ?)`), time.Now().UTC(), limit)
	if err != nil {
		return 0, err
	}
	n, err := x.RowsAffected()
	return int(n), err
}
