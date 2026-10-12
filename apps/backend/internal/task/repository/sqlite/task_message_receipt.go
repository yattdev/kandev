package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	"github.com/kandev/kandev/internal/db/dialect"
	"github.com/kandev/kandev/internal/orchestrator/messagequeue"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository/plancommenttx"
)

// FindOwnTaskMessageRecords reads only messages sent by one authenticated
// task/session in its workspace. Legacy marker matches remain ambiguity checked.
func (r *Repository) FindOwnTaskMessageRecords(ctx context.Context, workspace, senderTask, senderSession, targetTask, targetSession, intentID, messageID string) ([]models.TaskMessageRecord, error) {
	if workspace == "" || senderTask == "" || senderSession == "" || (intentID == "" && messageID == "") {
		return nil, errors.New("exact sender scope required")
	}
	records, err := r.findOwnTaskMessageKind(ctx, "message", workspace, senderTask, senderSession, targetTask, targetSession, intentID, messageID)
	if err != nil {
		return nil, err
	}
	queue, err := r.findOwnTaskMessageKind(ctx, "queue", workspace, senderTask, senderSession, targetTask, targetSession, intentID, messageID)
	return append(records, queue...), err
}

func (r *Repository) findOwnTaskMessageKind(ctx context.Context, kind, workspace, senderTask, senderSession, targetTask, targetSession, intentID, messageID string) ([]models.TaskMessageRecord, error) {
	table, session, metadata, created, turn := "task_session_messages", "task_session_id", "metadata", "created_at", "turn_id"
	author := " AND m.author_type = 'user' AND m.type = 'message'"
	if kind == "queue" {
		table, session, metadata, created, turn = "queued_messages", "session_id", "metadata_json", "queued_at", "''"
		author = ""
	}
	extract := func(key string) string { return dialect.JSONExtract(r.ro.DriverName(), "m."+metadata, key) }
	match := "m.id = ?"
	value := messageID
	if intentID != "" {
		contains := "instr(m.content, ?)"
		if dialect.IsPostgres(r.ro.DriverName()) {
			contains = "strpos(m.content, ?)"
		}
		match = fmt.Sprintf("(%s = ? OR (COALESCE(%s,'') = '' AND %s > 0))", extract("task_message_intent_id"), extract("task_message_intent_id"), contains)
		value = intentID
	}
	query := fmt.Sprintf(`SELECT m.id, m.task_id, m.%s, %s, m.%s AS receipt_metadata, m.%s AS receipt_created_at, m.content
 FROM %s m JOIN tasks t ON t.id = m.task_id WHERE t.workspace_id = ?
 AND %s = ? AND %s = ? AND (? = '' OR m.task_id = ?) AND (? = '' OR m.%s = ?)
 AND %s %s ORDER BY m.%s, m.id LIMIT 3`, session, turn, metadata, created, table, extract("sender_task_id"), extract("sender_session_id"), session, match, author, created)
	args := []any{workspace, senderTask, senderSession, targetTask, targetTask, targetSession, targetSession, value}
	if intentID != "" {
		args = append(args, intentID)
	}
	rows, err := r.ro.QueryContext(ctx, r.ro.Rebind(query), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanOwnTaskMessageRecords(rows, kind, intentID)
}

func scanOwnTaskMessageRecords(rows *sql.Rows, kind, intentID string) ([]models.TaskMessageRecord, error) {
	records := []models.TaskMessageRecord{}
	candidates := 0
	var legacyMarker *regexp.Regexp
	if intentID != "" {
		legacyMarker = regexp.MustCompile(`(?:^|[^A-Za-z0-9_.-])` + regexp.QuoteMeta(intentID) + `(?:$|[^A-Za-z0-9_.-])`)
	}
	for rows.Next() {
		candidates++
		if candidates > 2 {
			return nil, errors.New("ambiguous task message receipt")
		}
		record := models.TaskMessageRecord{Kind: kind}
		var raw, content string
		if err := rows.Scan(&record.ID, &record.TaskID, &record.SessionID, &record.TurnID, &raw, &record.CreatedAt, &content); err != nil {
			return nil, err
		}
		if len(raw) > 65536 || len(content) > 1048576 {
			return nil, errors.New("task message receipt exceeds bound")
		}
		if err := json.Unmarshal([]byte(raw), &record.Metadata); err != nil {
			return nil, err
		}
		if legacyMarker != nil && record.Metadata["task_message_intent_id"] != intentID && !legacyMarker.MatchString(content) {
			continue
		}
		digest := sha256.Sum256([]byte(content))
		record.ContentSHA256 = hex.EncodeToString(digest[:])
		records = append(records, record)
	}
	if len(records) > 2 {
		return nil, errors.New("ambiguous task message receipt")
	}
	return records, rows.Err()
}

// CreateTaskMessageAndQueue commits one transcript and durable delivery receipt
// under the existing task -> session writer fences, without a workflow move.
func (r *Repository) CreateTaskMessageAndQueue(ctx context.Context, message *models.Message, queued *messagequeue.QueuedMessage, expectedState models.TaskSessionState, max int) (err error) {
	fields, err := prepareQueuedPlanCommentMessage(message, queued)
	if err != nil {
		return err
	}
	originalMessage, originalQueued := *message, *queued
	tx, release, err := r.beginPlanCommentTx(ctx, message.TaskID)
	if err != nil {
		return err
	}
	defer release()
	defer func() {
		_ = tx.Rollback()
		if err != nil {
			*message = originalMessage
			*queued = originalQueued
		}
	}()
	if err := r.guardActivePlanCommentTaskTx(ctx, tx, message.TaskID); err != nil {
		return err
	}
	if err := lockSessionTurnWrites(ctx, tx, r.db.DriverName(), message.TaskSessionID); err != nil {
		return err
	}
	if err := plancommenttx.ValidateTargetSession(ctx, tx, r.db, message.TaskID, message.TaskSessionID, false, expectedState); err != nil {
		return err
	}
	var step, incarnation string
	if err := tx.QueryRowContext(ctx, r.db.Rebind(`SELECT COALESCE(t.workflow_step_id,''), s.queue_incarnation_id FROM tasks t JOIN task_sessions s ON s.task_id=t.id WHERE t.id=? AND s.id=?`), message.TaskID, message.TaskSessionID).Scan(&step, &incarnation); err != nil {
		return err
	}
	if step != message.Metadata["task_message_target_step"] || incarnation != message.Metadata["task_message_target_incarnation"] {
		return errors.New("task message route changed")
	}
	normalizedTime := dialect.NormalizedMicrosecond(r.db.DriverName(), "created_at")
	if err := r.assignUserMessageBoundary(ctx, tx, message, r.db.DriverName(), normalizedTime); err != nil {
		return err
	}
	if err := r.insertMessageRow(ctx, tx, message, fields.requestsInput, fields.messageType, fields.metadataJSON); err != nil {
		return err
	}
	if err := messagequeue.InsertTaskOwnedInTransaction(ctx, tx, r.db, queued, max); err != nil {
		return err
	}
	return tx.Commit()
}
