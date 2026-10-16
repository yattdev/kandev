package messagequeue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"
)

func (r *sqliteRepository) ObservePendingCensus(ctx context.Context, identity QueueSessionIdentity, executionID string, capacity int) (PendingCensus, error) {
	if err := ctx.Err(); err != nil {
		return PendingCensus{}, err
	}
	if !r.tasksTablePresent || !r.taskSessionsTaskIDPresent {
		return PendingCensus{}, ErrSessionIdentityMismatch
	}
	options := &sql.TxOptions{ReadOnly: true}
	if r.db.DriverName() == postgresDriverName {
		options.Isolation = sql.LevelRepeatableRead
	}
	tx, err := r.ro.BeginTxx(ctx, options)
	if err != nil {
		return PendingCensus{}, fmt.Errorf("begin pending census read: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := r.readCensusAuthorityTx(ctx, tx, identity); err != nil {
		return PendingCensus{}, err
	}
	if err := r.readCensusExecutionTx(ctx, tx, identity, executionID, false); err != nil {
		return PendingCensus{}, err
	}
	census := PendingCensus{
		Version: censusSnapshotVersion, Identity: identity, OriginalExecutionID: executionID,
		Capacity: capacity, Entries: make([]PendingCensusEntry, 0),
	}
	err = tx.GetContext(ctx, &census.Revision, r.db.Rebind(`
		SELECT census_revision FROM queue_session_locks WHERE session_id = ?
	`), identity.SessionID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return PendingCensus{}, fmt.Errorf("read pending census revision: %w", err)
	}
	if err := r.readCensusRowsTx(ctx, tx, &census); err != nil {
		return PendingCensus{}, err
	}
	census.Count = len(census.Entries)
	if err := census.Finalize(); err != nil {
		return PendingCensus{}, err
	}
	if err := tx.Commit(); err != nil {
		return PendingCensus{}, err
	}
	return census, nil
}

func (r *sqliteRepository) readCensusAuthorityTx(ctx context.Context, tx *sqlx.Tx, identity QueueSessionIdentity) error {
	var incarnation string
	query := `
		SELECT ts.queue_incarnation_id FROM task_sessions ts
		JOIN tasks t ON t.id = ts.task_id
		WHERE ts.id = ? AND ts.task_id = ? AND t.archived_at IS NULL
	`
	args := []any{identity.SessionID, identity.TaskID}
	if workspaceID := censusWorkspaceFence(ctx); workspaceID != "" {
		query += ` AND t.workspace_id = ?`
		args = append(args, workspaceID)
	}
	if ownerID := censusOwnerFence(ctx); ownerID != "" {
		query += ` AND EXISTS (SELECT 1 FROM workspaces w JOIN users u ON u.id = w.owner_id
			WHERE w.id = t.workspace_id AND w.owner_id = ? AND u.status = 'active')`
		args = append(args, ownerID)
	}
	err := tx.GetContext(ctx, &incarnation, r.db.Rebind(query), args...)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrSessionIdentityMismatch
	}
	if err != nil {
		return fmt.Errorf("read pending census authority: %w", err)
	}
	if incarnation == "" || incarnation != identity.SessionIncarnationID {
		return ErrSessionIdentityMismatch
	}
	return nil
}

func (r *sqliteRepository) readCensusExecutionTx(ctx context.Context, tx *sqlx.Tx, identity QueueSessionIdentity, executionID string, lock bool) error {
	if executionID == "" {
		return ErrSessionIdentityMismatch
	}
	query := `SELECT agent_execution_id FROM executors_running
		WHERE session_id = ? AND task_id = ? AND status IN ('starting', 'running', 'ready')`
	if lock && r.db.DriverName() == postgresDriverName {
		query += postgresForUpdateSuffix
	}
	var current string
	err := tx.GetContext(ctx, &current, r.db.Rebind(query), identity.SessionID, identity.TaskID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrSessionIdentityMismatch
	}
	if err != nil {
		return fmt.Errorf("read pending census execution: %w", err)
	}
	if current == "" || current != executionID {
		return ErrSessionIdentityMismatch
	}
	return nil
}

func (r *sqliteRepository) readCensusRowsTx(ctx context.Context, tx *sqlx.Tx, census *PendingCensus) error {
	rows, err := tx.QueryxContext(ctx, r.db.Rebind(`
		SELECT id, position, content, metadata_json, queued_at, queued_by
		FROM queued_messages WHERE session_id = ?
		ORDER BY position ASC, id ASC LIMIT ?
	`), census.Identity.SessionID, censusMaxStoredRows+1)
	if err != nil {
		return fmt.Errorf("read pending census rows: %w", err)
	}
	defer func() { _ = rows.Close() }()
	physical := 0
	for rows.Next() {
		physical++
		if physical > censusMaxStoredRows {
			return ErrCensusResourceExhausted
		}
		if physical%16 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		var message QueuedMessage
		var metadata string
		if err := rows.Scan(&message.ID, &message.Position, &message.Content, &metadata, &message.QueuedAt, &message.QueuedBy); err != nil {
			return fmt.Errorf("scan pending census row: %w", err)
		}
		reserved, err := isReservedMetadataJSON(metadata)
		if err != nil {
			return err
		}
		if reserved {
			continue
		}
		entry, err := NewPendingCensusEntry(census.Identity, census.OriginalExecutionID, census.Revision, message)
		if err != nil {
			return err
		}
		census.Entries = append(census.Entries, entry)
		if len(census.Entries) > censusMaxVisibleRows {
			return ErrCensusResourceExhausted
		}
	}
	return rows.Err()
}

func (r *sqliteRepository) DeleteClaimedPending(ctx context.Context, identity QueueSessionIdentity, executionID, entryID, claim string) (*QueueRemovalResult, error) {
	if !validCensusDigest(claim) {
		return nil, ErrQueueChanged
	}
	unlock := r.withSessionLock(identity.SessionID)
	defer unlock()
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin claimed queue removal: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	removed, retained, err := r.deleteClaimedPendingTx(ctx, tx, identity, executionID, entryID, claim)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &QueueRemovalResult{Removed: []QueuedMessage{*removed}, Retained: retained}, nil
}

func (r *sqliteRepository) deleteClaimedPendingTx(
	ctx context.Context, tx *sqlx.Tx, identity QueueSessionIdentity, executionID, entryID, claim string,
) (*QueuedMessage, []QueuedMessage, error) {
	if err := r.guardActiveTaskTx(ctx, tx, identity.TaskID); err != nil {
		return nil, nil, err
	}
	if workspaceID := censusWorkspaceFence(ctx); workspaceID != "" {
		var currentWorkspaceID string
		if err := tx.GetContext(ctx, &currentWorkspaceID, r.db.Rebind(`SELECT workspace_id FROM tasks WHERE id = ?`), identity.TaskID); err != nil {
			return nil, nil, ErrSessionIdentityMismatch
		}
		if currentWorkspaceID != workspaceID {
			return nil, nil, ErrSessionIdentityMismatch
		}
	}
	if err := r.checkCurrentCensusOwnerTx(ctx, tx, identity.TaskID); err != nil {
		return nil, nil, err
	}
	if err := r.lockSessionTx(ctx, tx, identity.SessionID); err != nil {
		return nil, nil, err
	}
	if err := r.validateSessionIdentityTx(ctx, tx, identity); err != nil {
		return nil, nil, err
	}
	if err := r.readCensusExecutionTx(ctx, tx, identity, executionID, true); err != nil {
		return nil, nil, err
	}
	if err := r.verifyClaimedRevisionTx(ctx, tx, identity, executionID, entryID, claim); err != nil {
		return nil, nil, err
	}
	message, metadataJSON, err := r.readClaimedPendingEntryTx(ctx, tx, identity.SessionID, entryID)
	if err != nil {
		return nil, nil, err
	}
	result, err := tx.ExecContext(ctx, r.db.Rebind(`
		DELETE FROM queued_messages WHERE id = ? AND session_id = ? AND metadata_json = ?
	`), entryID, identity.SessionID, metadataJSON)
	if err != nil {
		return nil, nil, fmt.Errorf("delete claimed queue entry: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return nil, nil, err
	}
	if affected != 1 {
		return nil, nil, ErrQueueChanged
	}
	if err := bumpCensusRevisionTx(ctx, tx, r.db, identity.SessionID); err != nil {
		return nil, nil, err
	}
	retained, err := listQueuedMessagesBySessionTx(ctx, r, tx, identity.SessionID)
	return message, retained, err
}

func (r *sqliteRepository) checkCurrentCensusOwnerTx(ctx context.Context, tx *sqlx.Tx, taskID string) error {
	ownerID := censusOwnerFence(ctx)
	if ownerID == "" {
		return nil
	}
	query := `SELECT w.owner_id FROM workspaces w JOIN tasks t ON t.workspace_id = w.id
		JOIN users u ON u.id = w.owner_id WHERE t.id = ? AND u.status = 'active'`
	if r.db.DriverName() == postgresDriverName {
		query += postgresForUpdateSuffix
	}
	var currentOwnerID string
	if err := tx.GetContext(ctx, &currentOwnerID, r.db.Rebind(query), taskID); err != nil || currentOwnerID != ownerID {
		return ErrSessionIdentityMismatch
	}
	return nil
}

func (r *sqliteRepository) verifyClaimedRevisionTx(
	ctx context.Context, tx *sqlx.Tx, identity QueueSessionIdentity, executionID, entryID, claim string,
) error {
	var revision int64
	if err := tx.GetContext(ctx, &revision, r.db.Rebind(`
		SELECT census_revision FROM queue_session_locks WHERE session_id = ?
	`), identity.SessionID); err != nil {
		return fmt.Errorf("read claimed queue revision: %w", err)
	}
	expected, err := CensusEntryClaim(identity, executionID, entryID, revision)
	if err != nil || expected != claim {
		return ErrQueueChanged
	}
	return nil
}

func (r *sqliteRepository) readClaimedPendingEntryTx(
	ctx context.Context, tx *sqlx.Tx, sessionID, entryID string,
) (*QueuedMessage, string, error) {
	row := tx.QueryRowxContext(ctx, r.db.Rebind(`
		SELECT id, session_id, task_id, position, content, model, plan_mode,
		       attachments_json, metadata_json, queued_at, queued_by
		FROM queued_messages WHERE id = ? AND session_id = ?
	`), entryID, sessionID)
	message, metadataJSON, err := scanQueuedRowWithMetadataJSON(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", ErrEntryNotFound
	}
	if err != nil {
		return nil, "", fmt.Errorf("read claimed queue entry: %w", err)
	}
	reserved, err := isReservedMetadataJSON(metadataJSON)
	if err != nil {
		return nil, "", err
	}
	if reserved {
		return nil, "", ErrEntryNotFound
	}
	blocked, err := r.editLeaseBlocksEntryTx(ctx, tx, sessionID, entryID)
	if err != nil {
		return nil, "", err
	}
	if blocked {
		return nil, "", ErrEditConflict
	}
	return message, metadataJSON, nil
}
