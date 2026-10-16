package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"

	internaldb "github.com/kandev/kandev/internal/db"
	"github.com/kandev/kandev/internal/db/dialect"
	"github.com/kandev/kandev/internal/orchestrator/messagequeue"
)

const accountAuthorityForUpdate = ` FOR UPDATE`

func (r *sqliteRepository) queueCensusAvailable(ctx context.Context) (bool, error) {
	present, err := internaldb.TableExistsContext(ctx, r.db, "queue_session_locks")
	if err != nil || !present {
		return false, err
	}
	return internaldb.ColumnExists(r.db, "queue_session_locks", "census_revision")
}

func (r *sqliteRepository) setAccountAuthorityField(ctx context.Context, id, column string, value any) error {
	present, err := r.queueCensusAvailable(ctx)
	if err != nil {
		return err
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	query := `SELECT ` + column + ` FROM users WHERE id = ?`
	if dialect.IsPostgres(r.db.DriverName()) {
		query += accountAuthorityForUpdate
	}
	var previous any
	if err := tx.QueryRowContext(ctx, r.db.Rebind(query), id).Scan(&previous); errors.Is(err, sql.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, r.db.Rebind(`UPDATE users SET `+column+` = ?, updated_at = ? WHERE id = ?`), value, time.Now().UTC(), id); err != nil {
		return err
	}
	if present && fmt.Sprint(previous) != fmt.Sprint(value) {
		if err := r.bumpOwnerQueueSessionsTx(ctx, tx, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *sqliteRepository) bumpOwnerQueueSessionsTx(ctx context.Context, tx *sqlx.Tx, ownerID string) error {
	var sessionIDs []string
	if err := tx.SelectContext(ctx, &sessionIDs, r.db.Rebind(`
		SELECT ts.id FROM task_sessions ts
		JOIN tasks t ON t.id = ts.task_id
		JOIN workspaces w ON w.id = t.workspace_id
		WHERE w.owner_id = ? ORDER BY ts.id
	`), ownerID); err != nil {
		return err
	}
	for _, sessionID := range sessionIDs {
		if err := messagequeue.LockAndBumpCensusRevisionTx(ctx, tx, r.db, sessionID); err != nil {
			return err
		}
	}
	return nil
}
