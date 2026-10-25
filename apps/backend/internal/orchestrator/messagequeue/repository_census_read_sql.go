package messagequeue

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/jmoiron/sqlx"
)

func (r *sqliteRepository) ActiveQueueCensus(ctx context.Context, proof ActiveQueueCensusProof, capacity int) (*ActiveQueueCensus, error) {
	if !r.canReadCensus(proof) {
		return nil, ErrSessionIdentityMismatch
	}
	options := &sql.TxOptions{ReadOnly: true}
	if r.ro.DriverName() == postgresDriverName {
		options.Isolation = sql.LevelRepeatableRead
	}
	tx, err := r.ro.BeginTxx(ctx, options)
	if err != nil {
		return nil, fmt.Errorf("begin queue census read: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := r.validateCensusSQLAuthority(ctx, tx, proof); err != nil {
		return nil, err
	}
	result, err := r.projectCensusSQL(ctx, tx, proof.Identity, capacity)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("finish queue census read: %w", err)
	}
	return result, nil
}

func (r *sqliteRepository) canReadCensus(proof ActiveQueueCensusProof) bool {
	identity := proof.Identity
	return r.ro != nil && r.tasksTablePresent && r.taskSessionsTaskIDPresent && r.queuedMessagesTaskIDPresent &&
		identity.TaskID != "" && identity.SessionID != "" && identity.SessionIncarnationID != "" &&
		proof.ExecutionID != "" && proof.WorkspaceID != ""
}

func (r *sqliteRepository) validateCensusSQLAuthority(ctx context.Context, tx *sqlx.Tx, proof ActiveQueueCensusProof) error {
	identity := proof.Identity
	var active int
	err := tx.GetContext(ctx, &active, r.ro.Rebind(`SELECT 1
		FROM tasks t JOIN task_sessions s ON s.task_id = t.id
		JOIN executors_running e ON e.session_id = s.id AND e.task_id = t.id
		WHERE t.id = ? AND t.workspace_id = ? AND t.archived_at IS NULL
		AND t.state NOT IN ('COMPLETED', 'FAILED', 'CANCELLED')
		AND s.id = ? AND s.queue_incarnation_id = ? AND s.state = 'RUNNING'
		AND e.agent_execution_id = ? AND e.status = 'running'
		AND NOT EXISTS (SELECT 1 FROM queue_session_transfer_compensations c
			WHERE c.task_id = t.id AND (c.from_session_id = s.id OR c.to_session_id = s.id))`),
		identity.TaskID, proof.WorkspaceID, identity.SessionID, identity.SessionIncarnationID, proof.ExecutionID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrSessionIdentityMismatch
	}
	if err != nil {
		return fmt.Errorf("validate queue census authority: %w", err)
	}
	return nil
}

func (r *sqliteRepository) projectCensusSQL(ctx context.Context, tx *sqlx.Tx, identity QueueSessionIdentity, capacity int) (*ActiveQueueCensus, error) {
	rows, err := tx.QueryxContext(ctx, r.ro.Rebind(censusHeadersQuery(r.ro.DriverName())), identity.SessionID)
	if err != nil {
		return nil, fmt.Errorf("read queue census headers: %w", err)
	}
	entries, ids, err := readCensusHeaders(rows, identity)
	if err != nil {
		return nil, err
	}
	if err := fillCensusSQLContent(ctx, tx, r.ro, identity.SessionID, ids, entries); err != nil {
		return nil, err
	}
	return newActiveQueueCensus(identity, capacity, entries)
}

func fillCensusSQLContent(ctx context.Context, tx *sqlx.Tx, db *sqlx.DB, sessionID string, ids []string, entries []ActiveQueueCensusEntry) error {
	if len(ids) == 0 {
		return nil
	}
	contents, err := readCensusContents(ctx, tx, db, sessionID, ids)
	if err != nil {
		return err
	}
	for i, id := range ids {
		content, ok := contents[id]
		if !ok {
			return ErrSessionIdentityMismatch
		}
		entry, err := censusEntry(id, content, i+1)
		if err != nil {
			return err
		}
		entries[i] = entry
	}
	return nil
}

func censusHeadersQuery(driver string) string {
	contentBytes := "length(CAST(content AS BLOB))"
	metadataBytes := "length(CAST(metadata_json AS BLOB))"
	metadataPrefix := "substr(CAST(metadata_json AS BLOB), 1, 32769)"
	idPrefix := "substr(CAST(id AS BLOB), 1, 257)"
	taskPrefix := "substr(CAST(task_id AS BLOB), 1, 257)"
	if driver == postgresDriverName {
		contentBytes = "octet_length(content)"
		metadataBytes = "octet_length(metadata_json)"
		metadataPrefix = "substring(convert_to(metadata_json, 'UTF8') from 1 for 32769)"
		idPrefix = "substring(convert_to(id, 'UTF8') from 1 for 257)"
		taskPrefix = "substring(convert_to(task_id, 'UTF8') from 1 for 257)"
	}
	return `SELECT ` + idPrefix + `, ` + taskPrefix + `, position, ` + contentBytes + `, ` + metadataBytes + `, ` + metadataPrefix + `
		FROM queued_messages WHERE session_id = ? ORDER BY position ASC, id ASC LIMIT 1025`
}

func readCensusHeaders(rows *sqlx.Rows, identity QueueSessionIdentity) ([]ActiveQueueCensusEntry, []string, error) {
	defer func() { _ = rows.Close() }()
	entries := make([]ActiveQueueCensusEntry, 0)
	ids := make([]string, 0)
	seen := make(map[string]struct{})
	budget := int64(maxCensusBytes)
	var previousPosition int64
	for rows.Next() {
		if len(seen) >= maxCensusExamined {
			return nil, nil, ErrCensusTooLarge
		}
		header, err := scanCensusSQLHeader(rows, identity)
		if err != nil {
			return nil, nil, err
		}
		if len(seen) > 0 && header.position < previousPosition {
			return nil, nil, ErrSessionIdentityMismatch
		}
		if _, ok := seen[header.id]; ok {
			return nil, nil, ErrSessionIdentityMismatch
		}
		seen[header.id] = struct{}{}
		previousPosition = header.position
		budget -= header.examinedBytes
		if budget < 0 {
			return nil, nil, ErrCensusTooLarge
		}
		if !header.visible {
			continue
		}
		if len(ids) >= maxCensusVisible {
			return nil, nil, ErrCensusTooLarge
		}
		ids = append(ids, header.id)
		entries = append(entries, ActiveQueueCensusEntry{})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate queue census headers: %w", err)
	}
	return entries, ids, nil
}

type censusSQLHeader struct {
	id            string
	position      int64
	examinedBytes int64
	visible       bool
}

func scanCensusSQLHeader(rows *sqlx.Rows, identity QueueSessionIdentity) (censusSQLHeader, error) {
	var idBytes, taskIDBytes, metadataValue []byte
	var position, contentBytes, metadataBytes int64
	if err := rows.Scan(&idBytes, &taskIDBytes, &position, &contentBytes, &metadataBytes, &metadataValue); err != nil {
		return censusSQLHeader{}, fmt.Errorf("scan queue census header: %w", err)
	}
	id := string(idBytes)
	if string(taskIDBytes) != identity.TaskID || position <= 0 {
		return censusSQLHeader{}, ErrSessionIdentityMismatch
	}
	if id == "" || !utf8.ValidString(id) || len(id) > maxCensusIDBytes {
		return censusSQLHeader{}, ErrCensusTooLarge
	}
	if metadataBytes < 0 || metadataBytes > maxCensusMetadataBytes || contentBytes < 0 {
		return censusSQLHeader{}, ErrCensusTooLarge
	}
	var fields map[string]interface{}
	if err := json.Unmarshal(metadataValue, &fields); err != nil {
		return censusSQLHeader{}, ErrSessionIdentityMismatch
	}
	message := QueuedMessage{Metadata: fields}
	visible := !message.IsReservedInFlight() || (message.IsDurablePlanComment() && !message.IsDeliveryAttempted())
	return censusSQLHeader{id: id, position: position, examinedBytes: contentBytes + metadataBytes, visible: visible}, nil
}

func readCensusContents(ctx context.Context, tx *sqlx.Tx, db *sqlx.DB, sessionID string, ids []string) (map[string]string, error) {
	query, args, err := sqlx.In(`SELECT id, content FROM queued_messages WHERE session_id = ? AND id IN (?)`, sessionID, ids)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryxContext(ctx, db.Rebind(query), args...)
	if err != nil {
		return nil, fmt.Errorf("read queue census content: %w", err)
	}
	defer func() { _ = rows.Close() }()
	contents := make(map[string]string, len(ids))
	for rows.Next() {
		var id, content string
		if err := rows.Scan(&id, &content); err != nil {
			return nil, fmt.Errorf("scan queue census content: %w", err)
		}
		contents[id] = content
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate queue census content: %w", err)
	}
	return contents, nil
}
