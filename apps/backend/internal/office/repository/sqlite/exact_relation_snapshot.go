package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/kandev/kandev/internal/db"
	"github.com/kandev/kandev/internal/db/dialect"
	"github.com/kandev/kandev/internal/exactsnapshotauthority"
	"github.com/kandev/kandev/internal/office/models"
)

const (
	exactRelationSnapshotDefaultTTL = 5 * time.Minute
	exactRelationSnapshotMaxTTL     = 15 * time.Minute
	exactRelationSnapshotMaxRows    = 500
	exactRelationSnapshotMaxPage    = 100
	exactRelationSnapshotCleanupMax = 100
)

var (
	// ErrExactRelationSnapshotUnavailable reports an expired, unknown, or
	// invalidated snapshot. Callers must materialize a fresh projection.
	ErrExactRelationSnapshotUnavailable = errors.New("exact relation snapshot unavailable")
	// ErrExactRelationCrossWorkspace rejects an edge whose endpoints do not
	// belong to the requested workspace.
	ErrExactRelationCrossWorkspace = errors.New("exact relation crosses workspace")
	// ErrExactRelationEndpointMissing rejects an orphaned blocker edge.
	ErrExactRelationEndpointMissing = errors.New("exact relation endpoint missing")
)

// initExactRelationSnapshotSchema is deliberately after Office migrations:
// migrateTaskPriorityToText rebuilds tasks and would otherwise remove fences.
func (r *Repository) initExactRelationSnapshotSchema() error {
	hasVersion, err := db.ColumnExists(r.db, "tasks", "resource_version")
	if err != nil {
		return fmt.Errorf("inspect task resource version: %w", err)
	}
	// Office-only installations retain the legacy task shape. Exact reads are
	// unavailable until the task repository has installed durable versions.
	if !hasVersion {
		return nil
	}
	if err := r.migrate.Apply("task_blockers.resource_version", `ALTER TABLE task_blockers ADD COLUMN resource_version INTEGER NOT NULL DEFAULT 1`); err != nil {
		return err
	}
	if err := r.migrate.Apply("exact_relation_snapshots.tables", `
		CREATE TABLE IF NOT EXISTS exact_relation_task_workspaces (task_id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, resource_version BIGINT NOT NULL);
		CREATE TABLE IF NOT EXISTS exact_relation_workspace_fences (workspace_id TEXT PRIMARY KEY, revision BIGINT NOT NULL DEFAULT 0);
		CREATE TABLE IF NOT EXISTS exact_relation_snapshots (token TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, workspace_revision BIGINT NOT NULL, expires_at TIMESTAMP NOT NULL);
		CREATE TABLE IF NOT EXISTS exact_relation_snapshot_rows (snapshot_token TEXT NOT NULL, ordinal INTEGER NOT NULL, task_id TEXT NOT NULL, blocker_task_id TEXT NOT NULL, workspace_id TEXT NOT NULL, task_resource_version BIGINT NOT NULL, blocker_resource_version BIGINT NOT NULL, resource_version BIGINT NOT NULL, created_at TIMESTAMP NOT NULL, PRIMARY KEY(snapshot_token, ordinal), UNIQUE(snapshot_token, task_id, blocker_task_id), FOREIGN KEY(snapshot_token) REFERENCES exact_relation_snapshots(token) ON DELETE CASCADE);
		CREATE INDEX IF NOT EXISTS idx_exact_relation_snapshots_expiry ON exact_relation_snapshots(expires_at);
	`); err != nil {
		return err
	}
	if dialect.IsPostgres(r.db.DriverName()) {
		_, err = r.db.Exec(`INSERT INTO exact_relation_task_workspaces(task_id, workspace_id, resource_version) SELECT id, workspace_id, resource_version FROM tasks ON CONFLICT(task_id) DO UPDATE SET workspace_id = EXCLUDED.workspace_id, resource_version = EXCLUDED.resource_version`)
	} else {
		_, err = r.db.Exec(`INSERT OR REPLACE INTO exact_relation_task_workspaces(task_id, workspace_id, resource_version) SELECT id, workspace_id, resource_version FROM tasks`)
	}
	if err != nil {
		return fmt.Errorf("backfill exact relation task mirrors: %w", err)
	}
	if dialect.IsPostgres(r.db.DriverName()) {
		return nil
	}
	if err := r.migrate.Apply("exact_relation_snapshots.fence_triggers", `
		CREATE TRIGGER exact_relation_task_fence_insert AFTER INSERT ON tasks BEGIN INSERT INTO exact_relation_task_workspaces(task_id,workspace_id,resource_version) VALUES(NEW.id,NEW.workspace_id,NEW.resource_version) ON CONFLICT(task_id) DO UPDATE SET workspace_id=excluded.workspace_id,resource_version=excluded.resource_version; INSERT INTO exact_relation_workspace_fences(workspace_id,revision) VALUES(NEW.workspace_id,1) ON CONFLICT(workspace_id) DO UPDATE SET revision=revision+1; END;
		CREATE TRIGGER exact_relation_task_fence_update AFTER UPDATE ON tasks BEGIN INSERT INTO exact_relation_task_workspaces(task_id,workspace_id,resource_version) VALUES(NEW.id,NEW.workspace_id,NEW.resource_version) ON CONFLICT(task_id) DO UPDATE SET workspace_id=excluded.workspace_id,resource_version=excluded.resource_version; INSERT INTO exact_relation_workspace_fences(workspace_id,revision) VALUES(NEW.workspace_id,1) ON CONFLICT(workspace_id) DO UPDATE SET revision=revision+1; INSERT INTO exact_relation_workspace_fences(workspace_id,revision) SELECT OLD.workspace_id,1 WHERE OLD.workspace_id<>NEW.workspace_id ON CONFLICT(workspace_id) DO UPDATE SET revision=revision+1; END;
		CREATE TRIGGER exact_relation_task_fence_delete AFTER DELETE ON tasks BEGIN DELETE FROM exact_relation_task_workspaces WHERE task_id=OLD.id; INSERT INTO exact_relation_workspace_fences(workspace_id,revision) VALUES(OLD.workspace_id,1) ON CONFLICT(workspace_id) DO UPDATE SET revision=revision+1; END;
		CREATE TRIGGER exact_relation_edge_fence_insert AFTER INSERT ON task_blockers BEGIN INSERT INTO exact_relation_workspace_fences(workspace_id,revision) SELECT workspace_id,1 FROM exact_relation_task_workspaces WHERE task_id=NEW.task_id ON CONFLICT(workspace_id) DO UPDATE SET revision=revision+1; END;
		CREATE TRIGGER exact_relation_edge_fence_delete AFTER DELETE ON task_blockers BEGIN INSERT INTO exact_relation_workspace_fences(workspace_id,revision) SELECT workspace_id,1 FROM exact_relation_task_workspaces WHERE task_id=OLD.task_id ON CONFLICT(workspace_id) DO UPDATE SET revision=revision+1; END;
		CREATE TRIGGER exact_relation_edge_version AFTER UPDATE ON task_blockers WHEN NEW.resource_version=OLD.resource_version BEGIN UPDATE task_blockers SET resource_version=OLD.resource_version+1 WHERE task_id=OLD.task_id AND blocker_task_id=OLD.blocker_task_id; END;
		CREATE TRIGGER exact_relation_edge_fence_update AFTER UPDATE ON task_blockers WHEN NEW.resource_version<>OLD.resource_version BEGIN INSERT INTO exact_relation_workspace_fences(workspace_id,revision) SELECT workspace_id,1 FROM exact_relation_task_workspaces WHERE task_id=NEW.task_id ON CONFLICT(workspace_id) DO UPDATE SET revision=revision+1; END;
	`); err != nil {
		return err
	}
	r.exactRelationSnapshotsEnabled = true
	return nil
}

func (r *Repository) OpenExactRelationSnapshot(ctx context.Context, request models.ExactRelationSnapshotRequest) (*models.ExactRelationSnapshot, error) {
	if _, err := r.CleanupExpiredExactRelationSnapshots(ctx, exactRelationSnapshotCleanupMax); err != nil {
		return nil, err
	}
	tx, err := r.BeginExactRelationSnapshotTx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	snapshot, err := r.OpenExactRelationSnapshotInTx(ctx, tx, request)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return snapshot, nil
}

// BeginExactRelationSnapshotTx starts the SQLite authority transaction a
// compositor must use. PostgreSQL stays fail-closed until it has conformance.
func (r *Repository) BeginExactRelationSnapshotTx(ctx context.Context) (*sqlx.Tx, error) {
	if !r.exactRelationSnapshotsEnabled || dialect.IsPostgres(r.db.DriverName()) {
		return nil, ErrExactRelationSnapshotUnavailable
	}
	return r.db.BeginTxx(ctx, nil)
}

// ValidateExactRelationSnapshotAuthority verifies the authority before a
// composite transaction is opened.
func (r *Repository) ValidateExactRelationSnapshotAuthority(authority *exactsnapshotauthority.Authority) error {
	if !r.exactRelationSnapshotsEnabled || dialect.IsPostgres(r.db.DriverName()) || !authority.Matches(r.db) {
		return ErrExactRelationSnapshotUnavailable
	}
	return nil
}

func (r *Repository) BeginExactRelationSnapshotAuthorityTx(ctx context.Context, authority *exactsnapshotauthority.Authority) (*exactsnapshotauthority.Transaction, error) {
	if err := r.ValidateExactRelationSnapshotAuthority(authority); err != nil {
		return nil, err
	}
	tx, err := authority.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return tx, nil
}

// OpenExactRelationSnapshotInAuthorityTx only accepts the sealed transaction
// issued by the matching shared SQLite authority.
func (r *Repository) OpenExactRelationSnapshotInAuthorityTx(ctx context.Context, authority *exactsnapshotauthority.Authority, tx *exactsnapshotauthority.Transaction, request models.ExactRelationSnapshotRequest) (*models.ExactRelationSnapshot, error) {
	if !r.exactRelationSnapshotsEnabled || dialect.IsPostgres(r.db.DriverName()) || !authority.Matches(r.db) || !tx.Matches(authority) {
		return nil, ErrExactRelationSnapshotUnavailable
	}
	return r.OpenExactRelationSnapshotInTx(ctx, tx.SQLX(), request)
}

// OpenExactRelationSnapshotInTx materializes one relation projection without
// committing it. Callers must use BeginExactRelationSnapshotTx; the wrapper
// preserves the legacy standalone API.
func (r *Repository) OpenExactRelationSnapshotInTx(ctx context.Context, tx *sqlx.Tx, request models.ExactRelationSnapshotRequest) (*models.ExactRelationSnapshot, error) {
	if !r.exactRelationSnapshotsEnabled || tx == nil || dialect.IsPostgres(r.db.DriverName()) {
		return nil, ErrExactRelationSnapshotUnavailable
	}
	ttl, err := exactRelationSnapshotTTL(request)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, r.db.Rebind(`INSERT INTO exact_relation_workspace_fences(workspace_id,revision) VALUES(?,0) ON CONFLICT(workspace_id) DO NOTHING`), request.WorkspaceID); err != nil {
		return nil, err
	}
	var revision int64
	if err = tx.QueryRowxContext(ctx, r.db.Rebind(`SELECT revision FROM exact_relation_workspace_fences WHERE workspace_id=?`), request.WorkspaceID).Scan(&revision); err != nil {
		return nil, err
	}
	items, err := r.materializeExactRelationSnapshot(ctx, tx, request.WorkspaceID)
	if err != nil {
		return nil, err
	}
	if len(items) > exactRelationSnapshotMaxRows {
		return nil, fmt.Errorf("exact relation snapshot exceeds %d edges", exactRelationSnapshotMaxRows)
	}
	expiresAt := time.Now().UTC().Add(ttl)
	token := uuid.NewString()
	if _, err = tx.ExecContext(ctx, r.db.Rebind(`INSERT INTO exact_relation_snapshots(token,workspace_id,workspace_revision,expires_at) VALUES(?,?,?,?)`), token, request.WorkspaceID, revision, expiresAt); err != nil {
		return nil, err
	}
	for i, item := range items {
		if _, err = tx.ExecContext(ctx, r.db.Rebind(`INSERT INTO exact_relation_snapshot_rows(snapshot_token,ordinal,task_id,blocker_task_id,workspace_id,task_resource_version,blocker_resource_version,resource_version,created_at) VALUES(?,?,?,?,?,?,?,?,?)`), token, i, item.TaskID, item.BlockerTaskID, item.WorkspaceID, item.TaskResourceVersion, item.BlockerResourceVersion, item.ResourceVersion, item.CreatedAt); err != nil {
			return nil, err
		}
	}
	return &models.ExactRelationSnapshot{Token: token, WorkspaceID: request.WorkspaceID, ExpiresAt: expiresAt}, nil
}

func exactRelationSnapshotTTL(request models.ExactRelationSnapshotRequest) (time.Duration, error) {
	if request.WorkspaceID == "" {
		return 0, fmt.Errorf("exact relation snapshot workspace is required")
	}
	ttl := request.TTL
	if ttl == 0 {
		ttl = exactRelationSnapshotDefaultTTL
	}
	if ttl < 0 || ttl > exactRelationSnapshotMaxTTL {
		return 0, fmt.Errorf("exact relation snapshot TTL is invalid")
	}
	return ttl, nil
}

func (r *Repository) materializeExactRelationSnapshot(ctx context.Context, tx *sqlx.Tx, workspaceID string) ([]models.ExactTaskRelation, error) {
	var invalid int
	if err := tx.QueryRowxContext(ctx, r.db.Rebind(`SELECT COUNT(*) FROM task_blockers b LEFT JOIN tasks t ON t.id=b.task_id LEFT JOIN tasks p ON p.id=b.blocker_task_id WHERE (t.workspace_id=? OR p.workspace_id=?) AND (t.id IS NULL OR p.id IS NULL)`), workspaceID, workspaceID).Scan(&invalid); err != nil {
		return nil, err
	}
	if invalid > 0 {
		return nil, ErrExactRelationEndpointMissing
	}
	if err := tx.QueryRowxContext(ctx, r.db.Rebind(`SELECT COUNT(*) FROM task_blockers b JOIN tasks t ON t.id=b.task_id JOIN tasks p ON p.id=b.blocker_task_id WHERE (t.workspace_id=? OR p.workspace_id=?) AND t.workspace_id<>p.workspace_id`), workspaceID, workspaceID).Scan(&invalid); err != nil {
		return nil, err
	}
	if invalid > 0 {
		return nil, ErrExactRelationCrossWorkspace
	}
	rows, err := tx.QueryxContext(ctx, r.db.Rebind(`SELECT b.task_id,b.blocker_task_id,t.workspace_id,t.resource_version,p.resource_version,b.resource_version,b.created_at FROM task_blockers b JOIN tasks t ON t.id=b.task_id JOIN tasks p ON p.id=b.blocker_task_id WHERE t.workspace_id=? AND p.workspace_id=? ORDER BY b.created_at,b.task_id,b.blocker_task_id LIMIT ?`), workspaceID, workspaceID, exactRelationSnapshotMaxRows+1)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]models.ExactTaskRelation, 0)
	for rows.Next() {
		var item models.ExactTaskRelation
		if err := rows.Scan(&item.TaskID, &item.BlockerTaskID, &item.WorkspaceID, &item.TaskResourceVersion, &item.BlockerResourceVersion, &item.ResourceVersion, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) PageExactRelationSnapshot(ctx context.Context, token string, offset, limit int) ([]models.ExactTaskRelation, error) {
	if !r.exactRelationSnapshotsEnabled {
		return nil, ErrExactRelationSnapshotUnavailable
	}
	if offset < 0 || limit < 1 || limit > exactRelationSnapshotMaxPage {
		return nil, fmt.Errorf("exact relation snapshot page is invalid")
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := r.requireExactRelationSnapshotTx(ctx, tx, token); err != nil {
		return nil, err
	}
	if r.exactRelationSnapshotReadAfterFenceHook != nil {
		r.exactRelationSnapshotReadAfterFenceHook()
	}
	var items []models.ExactTaskRelation
	err = tx.SelectContext(ctx, &items, r.db.Rebind(`SELECT task_id,blocker_task_id,workspace_id,task_resource_version,blocker_resource_version,resource_version,created_at FROM exact_relation_snapshot_rows WHERE snapshot_token=? AND ordinal>=? ORDER BY ordinal LIMIT ?`), token, offset, limit)
	if err != nil {
		return nil, err
	}
	return items, tx.Commit()
}
func (r *Repository) GetExactRelationSnapshotRelation(ctx context.Context, token, taskID, blockerTaskID string) (*models.ExactTaskRelation, error) {
	if !r.exactRelationSnapshotsEnabled {
		return nil, ErrExactRelationSnapshotUnavailable
	}
	if taskID == "" || blockerTaskID == "" {
		return nil, fmt.Errorf("exact relation endpoint is required")
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := r.requireExactRelationSnapshotTx(ctx, tx, token); err != nil {
		return nil, err
	}
	if r.exactRelationSnapshotReadAfterFenceHook != nil {
		r.exactRelationSnapshotReadAfterFenceHook()
	}
	var item models.ExactTaskRelation
	err = tx.GetContext(ctx, &item, r.db.Rebind(`SELECT task_id,blocker_task_id,workspace_id,task_resource_version,blocker_resource_version,resource_version,created_at FROM exact_relation_snapshot_rows WHERE snapshot_token=? AND task_id=? AND blocker_task_id=?`), token, taskID, blockerTaskID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &item, nil
}
func (r *Repository) requireExactRelationSnapshotTx(ctx context.Context, tx *sqlx.Tx, token string) error {
	var expires time.Time
	var snapshotWorkspace string
	var snapshotRevision, current int64
	err := tx.QueryRowxContext(ctx, r.db.Rebind(`SELECT s.workspace_id,s.workspace_revision,s.expires_at,f.revision FROM exact_relation_snapshots s JOIN exact_relation_workspace_fences f ON f.workspace_id=s.workspace_id WHERE s.token=?`), token).Scan(&snapshotWorkspace, &snapshotRevision, &expires, &current)
	if err != nil || time.Now().UTC().Compare(expires) >= 0 || snapshotRevision != current {
		return ErrExactRelationSnapshotUnavailable
	}
	return nil
}
func (r *Repository) CleanupExpiredExactRelationSnapshots(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > exactRelationSnapshotCleanupMax {
		return 0, fmt.Errorf("exact relation snapshot cleanup limit is invalid")
	}
	result, err := r.db.ExecContext(ctx, r.db.Rebind(`DELETE FROM exact_relation_snapshots WHERE token IN (SELECT token FROM exact_relation_snapshots WHERE expires_at<=? ORDER BY expires_at,token LIMIT ?)`), time.Now().UTC(), limit)
	if err != nil {
		return 0, err
	}
	n, err := result.RowsAffected()
	return int(n), err
}
