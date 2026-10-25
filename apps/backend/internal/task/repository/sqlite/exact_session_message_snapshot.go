package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/kandev/kandev/internal/db/dialect"
	"github.com/kandev/kandev/internal/sysprompt"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository/repoerrors"
)

// initExactSessionMessageSnapshotSchema deliberately leaves PostgreSQL without
// this authority until its equivalent transactional identity proof exists.
func (r *Repository) initExactSessionMessageSnapshotSchema() error {
	if dialect.IsPostgres(r.db.DriverName()) {
		return nil
	}
	return r.migrate.Apply("exact_session_message_snapshots.tables", `
		CREATE TABLE IF NOT EXISTS exact_session_message_snapshots (
			token TEXT PRIMARY KEY,
			installation_id TEXT NOT NULL,
			workspace_id TEXT NOT NULL,
			task_id TEXT NOT NULL,
			session_id TEXT NOT NULL,
			queue_incarnation_id TEXT NOT NULL,
			route_generation BIGINT NOT NULL,
			session_resource_version BIGINT NOT NULL,
			expires_at TIMESTAMP NOT NULL
		);
		CREATE TABLE IF NOT EXISTS exact_session_message_snapshot_rows (
			snapshot_token TEXT NOT NULL,
			ordinal INTEGER NOT NULL,
			message_id TEXT NOT NULL,
			author_type TEXT NOT NULL,
			content TEXT NOT NULL,
			type TEXT NOT NULL,
			requests_input INTEGER NOT NULL,
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL,
			PRIMARY KEY (snapshot_token, ordinal),
			UNIQUE (snapshot_token, message_id),
			FOREIGN KEY (snapshot_token) REFERENCES exact_session_message_snapshots(token) ON DELETE CASCADE
		);
		CREATE INDEX IF NOT EXISTS idx_exact_session_message_snapshots_expiry ON exact_session_message_snapshots(expires_at);
	`)
}

func (r *Repository) OpenExactSessionMessageSnapshot(ctx context.Context, request models.ExactSessionMessageSnapshotRequest) (*models.ExactSessionMessageSnapshot, error) {
	if dialect.IsPostgres(r.db.DriverName()) {
		return nil, repoerrors.ErrExactSessionMessageSnapshotUnavailable
	}
	if err := validateExactSessionMessageIdentity(request); err != nil {
		return nil, err
	}
	ttl := request.TTL
	if ttl == 0 {
		ttl = exactTaskSnapshotDefaultTTL
	}
	if ttl < 0 || ttl > exactTaskSnapshotMaxTTL {
		return nil, fmt.Errorf("exact session message snapshot TTL is invalid")
	}
	if _, err := r.CleanupExpiredExactSessionMessageSnapshots(ctx, exactTaskSnapshotCleanupMax); err != nil {
		return nil, fmt.Errorf("cleanup expired exact session message snapshots: %w", err)
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := r.requireExactSessionMessageIdentityTx(ctx, tx, request); err != nil {
		return nil, err
	}
	rows, err := r.materializeExactSessionMessageSnapshotRows(ctx, tx, request.SessionID, request.TaskID)
	if err != nil {
		return nil, err
	}
	if len(rows) > exactTaskSnapshotMaxRows {
		return nil, fmt.Errorf("exact session message snapshot exceeds %d messages", exactTaskSnapshotMaxRows)
	}
	snapshot := models.ExactSessionMessageSnapshot{
		Token: uuid.NewString(), InstallationID: request.InstallationID, WorkspaceID: request.WorkspaceID,
		TaskID: request.TaskID, SessionID: request.SessionID, QueueIncarnationID: request.QueueIncarnationID,
		RouteGeneration: request.RouteGeneration, SessionResourceVersion: request.SessionResourceVersion,
		ExpiresAt: r.nowUTC().Add(ttl),
	}
	if _, err = tx.ExecContext(ctx, r.db.Rebind(`INSERT INTO exact_session_message_snapshots(token, installation_id, workspace_id, task_id, session_id, queue_incarnation_id, route_generation, session_resource_version, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`), snapshot.Token, snapshot.InstallationID, snapshot.WorkspaceID, snapshot.TaskID, snapshot.SessionID, snapshot.QueueIncarnationID, snapshot.RouteGeneration, snapshot.SessionResourceVersion, snapshot.ExpiresAt); err != nil {
		return nil, err
	}
	for ordinal, row := range rows {
		if _, err = tx.ExecContext(ctx, r.db.Rebind(`INSERT INTO exact_session_message_snapshot_rows(snapshot_token, ordinal, message_id, author_type, content, type, requests_input, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`), snapshot.Token, ordinal, row.ID, row.AuthorType, row.Content, row.Type, boolInt(row.RequestsInput), row.CreatedAt, row.UpdatedAt); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &snapshot, nil
}

func validateExactSessionMessageIdentity(request models.ExactSessionMessageSnapshotRequest) error {
	if request.InstallationID == "" || request.WorkspaceID == "" || request.TaskID == "" || request.SessionID == "" || request.QueueIncarnationID == "" || request.SessionResourceVersion <= 0 {
		return fmt.Errorf("exact session message snapshot identity is incomplete")
	}
	return nil
}

func (r *Repository) requireExactSessionMessageIdentityTx(ctx context.Context, tx *sqlx.Tx, request models.ExactSessionMessageSnapshotRequest) error {
	var found int
	err := tx.QueryRowxContext(ctx, r.db.Rebind(`SELECT 1 FROM task_sessions ts JOIN tasks t ON t.id = ts.task_id WHERE ts.id = ? AND ts.task_id = ? AND t.workspace_id = ? AND ts.queue_incarnation_id = ? AND ts.route_generation = ? AND ts.resource_version = ?`), request.SessionID, request.TaskID, request.WorkspaceID, request.QueueIncarnationID, request.RouteGeneration, request.SessionResourceVersion).Scan(&found)
	if err == sql.ErrNoRows {
		return repoerrors.ErrExactSessionMessageSnapshotUnavailable
	}
	return err
}

func (r *Repository) materializeExactSessionMessageSnapshotRows(ctx context.Context, tx *sqlx.Tx, sessionID, taskID string) ([]models.ExactSessionMessageSnapshotMessage, error) {
	rows, err := tx.QueryxContext(ctx, r.db.Rebind(`SELECT id, author_type, content, type, requests_input, created_at, updated_at FROM task_session_messages WHERE task_session_id = ? AND task_id = ? ORDER BY created_at, id LIMIT ?`), sessionID, taskID, exactTaskSnapshotMaxRows+1)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := make([]models.ExactSessionMessageSnapshotMessage, 0)
	for rows.Next() {
		var row models.ExactSessionMessageSnapshotMessage
		var requestsInput int
		if err := rows.Scan(&row.ID, &row.AuthorType, &row.Content, &row.Type, &requestsInput, &row.CreatedAt, &row.UpdatedAt); err != nil {
			return nil, err
		}
		row.Content = sysprompt.StripSystemContent(row.Content)
		row.RequestsInput = requestsInput == 1
		result = append(result, row)
	}
	return result, rows.Err()
}

func (r *Repository) PageExactSessionMessageSnapshot(ctx context.Context, request models.ExactSessionMessageSnapshotPageRequest) ([]models.ExactSessionMessageSnapshotMessage, error) {
	if dialect.IsPostgres(r.db.DriverName()) {
		return nil, repoerrors.ErrExactSessionMessageSnapshotUnavailable
	}
	if err := validateExactSessionMessageIdentity(request.ExactSessionMessageSnapshotRequest); err != nil || request.Token == "" || request.Offset < 0 || request.Limit < 1 || request.Limit > exactTaskSnapshotMaxPage {
		return nil, repoerrors.ErrExactSessionMessageSnapshotUnavailable
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var found int
	err = tx.QueryRowxContext(ctx, r.db.Rebind(`SELECT 1 FROM exact_session_message_snapshots WHERE token = ? AND installation_id = ? AND workspace_id = ? AND task_id = ? AND session_id = ? AND queue_incarnation_id = ? AND route_generation = ? AND session_resource_version = ? AND expires_at > ?`), request.Token, request.InstallationID, request.WorkspaceID, request.TaskID, request.SessionID, request.QueueIncarnationID, request.RouteGeneration, request.SessionResourceVersion, r.nowUTC()).Scan(&found)
	if err == sql.ErrNoRows {
		return nil, repoerrors.ErrExactSessionMessageSnapshotUnavailable
	}
	if err != nil {
		return nil, err
	}
	if err := r.requireExactSessionMessageIdentityTx(ctx, tx, request.ExactSessionMessageSnapshotRequest); err != nil {
		return nil, err
	}
	rows, err := tx.QueryxContext(ctx, r.db.Rebind(`SELECT message_id, author_type, content, type, requests_input, created_at, updated_at FROM exact_session_message_snapshot_rows WHERE snapshot_token = ? AND ordinal >= ? ORDER BY ordinal LIMIT ?`), request.Token, request.Offset, request.Limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := make([]models.ExactSessionMessageSnapshotMessage, 0)
	for rows.Next() {
		var row models.ExactSessionMessageSnapshotMessage
		var requestsInput int
		if err := rows.Scan(&row.ID, &row.AuthorType, &row.Content, &row.Type, &requestsInput, &row.CreatedAt, &row.UpdatedAt); err != nil {
			return nil, err
		}
		row.RequestsInput = requestsInput == 1
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, tx.Commit()
}

func (r *Repository) CleanupExpiredExactSessionMessageSnapshots(ctx context.Context, limit int) (int, error) {
	if dialect.IsPostgres(r.db.DriverName()) {
		return 0, repoerrors.ErrExactSessionMessageSnapshotUnavailable
	}
	if limit < 1 || limit > exactTaskSnapshotCleanupMax {
		return 0, fmt.Errorf("exact session message snapshot cleanup limit is invalid")
	}
	result, err := r.db.ExecContext(ctx, r.db.Rebind(`DELETE FROM exact_session_message_snapshots WHERE expires_at <= ? AND token IN (SELECT token FROM exact_session_message_snapshots WHERE expires_at <= ? ORDER BY expires_at, token LIMIT ?)`), r.nowUTC(), r.nowUTC(), limit)
	if err != nil {
		return 0, err
	}
	count, err := result.RowsAffected()
	return int(count), err
}

var _ interface {
	OpenExactSessionMessageSnapshot(context.Context, models.ExactSessionMessageSnapshotRequest) (*models.ExactSessionMessageSnapshot, error)
	PageExactSessionMessageSnapshot(context.Context, models.ExactSessionMessageSnapshotPageRequest) ([]models.ExactSessionMessageSnapshotMessage, error)
	CleanupExpiredExactSessionMessageSnapshots(context.Context, int) (int, error)
} = (*Repository)(nil)
