package messagequeue

import (
	"context"
	"database/sql"
	"fmt"
	"math"

	"github.com/jmoiron/sqlx"
)

var errCensusRevisionExhausted = fmt.Errorf("queue census revision exhausted")

func (r *memoryRepository) canBumpCensusLocked(sessionID string) error {
	if r.censusRevision[sessionID] == math.MaxInt64 {
		return errCensusRevisionExhausted
	}
	return nil
}

func (r *memoryRepository) bumpCensusLocked(sessionID string) {
	r.censusRevision[sessionID]++
}

// CensusRevisionTx is the transaction capability shared with task lifecycle
// writers that already hold the queue session lock.
type CensusRevisionTx interface {
	ExecContext(context.Context, string, ...interface{}) (sql.Result, error)
}

func BumpCensusRevisionTx(ctx context.Context, tx CensusRevisionTx, db *sqlx.DB, sessionID string) error {
	result, err := tx.ExecContext(ctx, db.Rebind(`
		UPDATE queue_session_locks
		SET census_revision = census_revision + 1
		WHERE session_id = ? AND census_revision >= 0 AND census_revision < ?
	`), sessionID, int64(math.MaxInt64))
	if err != nil {
		return fmt.Errorf("advance queue census revision: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return errCensusRevisionExhausted
	}
	return nil
}

// LockAndBumpCensusRevisionTx lets task lifecycle transactions invalidate a
// session even when it has no queue row or prior lock row.
func LockAndBumpCensusRevisionTx(ctx context.Context, tx CensusRevisionTx, db *sqlx.DB, sessionID string) error {
	if _, err := tx.ExecContext(ctx, db.Rebind(`
		INSERT INTO queue_session_locks (session_id) VALUES (?)
		ON CONFLICT(session_id) DO UPDATE SET session_id = excluded.session_id
	`), sessionID); err != nil {
		return fmt.Errorf("lock queue census session: %w", err)
	}
	return BumpCensusRevisionTx(ctx, tx, db, sessionID)
}

func bumpCensusRevisionTx(ctx context.Context, tx *sqlx.Tx, db *sqlx.DB, sessionID string) error {
	return BumpCensusRevisionTx(ctx, tx, db, sessionID)
}
