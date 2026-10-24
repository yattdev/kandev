package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jmoiron/sqlx"

	"github.com/kandev/kandev/internal/db/dialect"
	"github.com/kandev/kandev/internal/task/models"
)

type parentRetentionKey struct{}

type parentRetentionAuthority struct {
	parentID, workspaceID string
	held                  bool
}

// UpdateTaskWithParentRetention performs the ordinary task update while
// authorizing the hold against the live parent/workspace row in that same
// transaction. Other full-document writers preserve the live hold.
func (r *Repository) UpdateTaskWithParentRetention(
	ctx context.Context, task *models.Task, parentID, workspaceID string, held, explicitPosition bool,
) error {
	if parentID == "" || workspaceID == "" || parentID == task.ID {
		return errors.New("direct parent retention authority is required")
	}
	metadata, err := json.Marshal(task.Metadata)
	if err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := r.lockParentRetentionRows(ctx, tx, parentID, task.ID); err != nil {
		return err
	}
	if err := r.validateRetentionParent(ctx, tx, parentID, workspaceID); err != nil {
		return err
	}
	ctx = context.WithValue(ctx, parentRetentionKey{}, parentRetentionAuthority{
		parentID: parentID, workspaceID: workspaceID, held: held,
	})
	entryID, markerEntryID, err := r.updateTaskTx(ctx, tx, task, metadata, "", !explicitPosition, true, nil)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	r.dispatchStepEntry(ctx, task.ID, task.WorkflowID, task.WorkflowStepID, entryID, markerEntryID)
	return nil
}

func (r *Repository) lockParentRetentionRows(ctx context.Context, tx *sql.Tx, parentID, taskID string) error {
	ids := []string{parentID, taskID}
	sort.Strings(ids)
	for _, id := range ids {
		if !dialect.IsPostgres(r.db.DriverName()) {
			result, err := tx.ExecContext(ctx, r.db.Rebind(`
				UPDATE tasks SET updated_at = updated_at WHERE id = ?
			`), id)
			if err != nil {
				return err
			}
			if count, err := result.RowsAffected(); err != nil || count != 1 {
				return errors.New("direct parent retention task is unavailable")
			}
		} else {
			var lockedID string
			if err := tx.QueryRowContext(ctx, r.db.Rebind(`
				SELECT id FROM tasks WHERE id = ? FOR UPDATE
			`), id).Scan(&lockedID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *Repository) validateRetentionParent(ctx context.Context, tx *sql.Tx, parentID, workspaceID string) error {
	var parentWorkspaceID, parentOrigin string
	if err := tx.QueryRowContext(ctx, r.db.Rebind(`
		SELECT COALESCE(workspace_id, ''), COALESCE(origin, '') FROM tasks WHERE id = ?
	`), parentID).Scan(&parentWorkspaceID, &parentOrigin); err != nil {
		return err
	}
	if parentWorkspaceID != workspaceID || parentOrigin == models.TaskOriginAutomationRun {
		return errors.New("direct parent retention authority changed")
	}
	return nil
}

func (r *Repository) preserveLiveRetentionInTx(
	ctx context.Context, tx *sql.Tx, taskID string, outgoing []byte,
) ([]byte, error) {
	var raw sql.NullString
	var parentID, workspaceID, origin string
	if err := tx.QueryRowContext(ctx, r.db.Rebind(`
		SELECT metadata, COALESCE(parent_id, ''), COALESCE(workspace_id, ''), COALESCE(origin, '')
		FROM tasks WHERE id = ?
	`), taskID).Scan(&raw, &parentID, &workspaceID, &origin); err != nil {
		return nil, err
	}
	current, err := parseRetentionDocument(raw.String)
	if err != nil {
		return nil, err
	}
	payload, err := parseRetentionDocument(string(outgoing))
	if err != nil {
		return nil, err
	}
	if authority, ok := ctx.Value(parentRetentionKey{}).(parentRetentionAuthority); ok {
		if err := r.applyAuthorizedRetentionInTx(ctx, tx, taskID, parentID, workspaceID, origin, authority, payload); err != nil {
			return nil, err
		}
	} else if value, ok := current[models.MetaKeyTerminalRetention]; ok {
		payload[models.MetaKeyTerminalRetention] = value
	} else {
		delete(payload, models.MetaKeyTerminalRetention)
	}
	return json.Marshal(payload)
}

func (r *Repository) applyAuthorizedRetentionInTx(
	ctx context.Context, tx *sql.Tx, taskID, parentID, workspaceID, origin string,
	authority parentRetentionAuthority, payload map[string]json.RawMessage,
) error {
	if parentID != authority.parentID || workspaceID != authority.workspaceID ||
		origin == models.TaskOriginAutomationRun || parentID == taskID {
		return errors.New("direct parent retention authority changed")
	}
	if authority.held {
		var history bool
		if err := tx.QueryRowContext(ctx, r.db.Rebind(`
			SELECT EXISTS (SELECT 1 FROM task_resource_cleanup_jobs WHERE task_id = ?)
		`), taskID).Scan(&history); err != nil {
			return err
		}
		if history {
			return errors.New("task cleanup history prevents retention admission")
		}
	}
	payload[models.MetaKeyTerminalRetention] = json.RawMessage(fmt.Sprintf("%t", authority.held))
	return nil
}

func parseRetentionDocument(raw string) (map[string]json.RawMessage, error) {
	if strings.TrimSpace(raw) == "" || raw == "null" {
		return make(map[string]json.RawMessage), nil
	}
	var metadata map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil || metadata == nil {
		return nil, errors.New("invalid task metadata for retention")
	}
	if value, ok := metadata[models.MetaKeyTerminalRetention]; ok {
		if string(value) != metadataTrueString && string(value) != "false" {
			return nil, errors.New("invalid task terminal retention value")
		}
	}
	return metadata, nil
}

var errTaskRetentionHeld = errors.New("task terminal retention is held")
var errCleanupReservationConflict = errors.New("task cleanup reservation conflicts with its existing operation")
var errReservedRetentionMetadata = errors.New("terminal retention requires the guarded parent operation")

func rejectRetentionMetadataKey(key string) error {
	if key == models.MetaKeyTerminalRetention {
		return errReservedRetentionMetadata
	}
	return nil
}

// taskRetentionInTx reads the live metadata after the caller has serialized on
// the task row. Malformed metadata cannot be treated as an absent hold.
func (r *Repository) taskRetentionInTx(ctx context.Context, tx *sqlx.Tx, taskID string) (held, found bool, err error) {
	var raw sql.NullString
	err = tx.QueryRowContext(ctx, r.db.Rebind(`SELECT metadata FROM tasks WHERE id = ?`), taskID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	if !raw.Valid || strings.TrimSpace(raw.String) == "" || raw.String == "null" {
		return false, true, nil
	}
	var metadata map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw.String), &metadata); err != nil {
		return false, true, fmt.Errorf("invalid task metadata for retention admission: %w", err)
	}
	if metadata == nil {
		return false, true, errors.New("invalid task metadata for retention admission: null object")
	}
	value, exists := metadata[models.MetaKeyTerminalRetention]
	if !exists {
		return false, true, nil
	}
	if string(value) != metadataTrueString && string(value) != "false" {
		return false, true, errors.New("invalid terminal retention value")
	}
	return string(value) == metadataTrueString, true, nil
}

func (r *Repository) ensureTaskNotHeldInTx(ctx context.Context, tx *sqlx.Tx, taskID string) error {
	held, _, err := r.taskRetentionInTx(ctx, tx, taskID)
	if err != nil {
		return err
	}
	if held {
		return fmt.Errorf("%w: %s", errTaskRetentionHeld, taskID)
	}
	return nil
}

// lockCleanupTaskInTx takes the task row before any cleanup-job update. The
// job lookup only locates its immutable task identity; the job state is
// checked again by the caller's conditional UPDATE after the task lock.
func (r *Repository) lockCleanupTaskInTx(ctx context.Context, tx *sqlx.Tx, jobID string) (string, error) {
	var taskID string
	var trigger models.TaskResourceCleanupTrigger
	if err := tx.QueryRowContext(ctx, r.db.Rebind(`
		SELECT task_id, trigger FROM task_resource_cleanup_jobs WHERE id = ?
	`), jobID).Scan(&taskID, &trigger); err != nil {
		return "", err
	}
	if taskID == "" {
		if trigger != models.TaskResourceCleanupTriggerWorkspaceDelete {
			return "", errors.New("task cleanup job has no task identity")
		}
		return "", nil
	}
	if !dialect.IsPostgres(r.db.DriverName()) {
		// A SQLite deferred transaction needs a write reservation before reading
		// task metadata; this update does not alter the persisted timestamp.
		if _, err := tx.ExecContext(ctx, r.db.Rebind(`
			UPDATE tasks SET updated_at = updated_at WHERE id = ?
		`), taskID); err != nil {
			return "", err
		}
	} else if err := r.lockTaskRowInTx(ctx, tx, taskID); err != nil && !errors.Is(err, ErrTaskNotFound) {
		return "", err
	}
	held, _, err := r.taskRetentionInTx(ctx, tx, taskID)
	if err != nil {
		return "", err
	}
	if held {
		return "", fmt.Errorf("%w: %s", errTaskRetentionHeld, taskID)
	}
	return taskID, nil
}
