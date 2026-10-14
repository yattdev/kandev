package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/kandev/kandev/internal/db/dialect"
	"github.com/kandev/kandev/internal/task/models"
)

func (r *Repository) validateCompletionClaimTurn(ctx context.Context, tx *sql.Tx, taskID, sessionID string, value interface{}) error {
	signal, ok := value.(models.PendingStepCompletionSignal)
	if !ok || signal.TurnID == "" {
		return nil
	}
	var latest, metadata string
	query := fmt.Sprintf(`SELECT id, metadata FROM task_session_turns turn_row WHERE task_session_id = ? AND %s ORDER BY started_at DESC, created_at DESC, id DESC LIMIT 1`, turnHistoryPredicate(r.db.DriverName(), "turn_row"))
	if err := tx.QueryRowContext(ctx, r.db.Rebind(query), sessionID).Scan(&latest, &metadata); err != nil {
		return err
	}
	if latest != signal.TurnID {
		return models.ErrStaleCompletionTurn
	}
	var bag map[string]interface{}
	if err := json.Unmarshal([]byte(metadata), &bag); err != nil {
		return err
	}
	current, err := r.completionGeneration(ctx, tx, taskID)
	if err != nil {
		return err
	}
	if models.StringFromAny(bag[models.TurnMetaKeyStepGenerationAtStart]) != current {
		return models.ErrStaleCompletionTurn
	}

	return nil
}

func (r *Repository) writeCompletionClaim(ctx context.Context, tx *sql.Tx, sessionID, key, stepID string, value interface{}, valueJSON []byte) (int64, error) {
	now := time.Now().UTC()
	driver := r.db.DriverName()
	path := key
	stepPath := key
	if !dialect.IsPostgres(driver) {
		path = "$." + key
		stepPath = path + ".step_id"
	}
	query := setSessionMetadataKeyIfAbsentOrDifferentStepQuery(driver)
	identity := stepID
	if signal, ok := value.(models.PendingStepCompletionSignal); ok && signal.TurnID != "" {
		identity = signal.TurnID
		if dialect.IsPostgres(driver) {
			query = strings.ReplaceAll(query, "'step_id'", "'turn_id'")
		} else {
			stepPath = path + ".turn_id"
		}
	}
	result, err := tx.ExecContext(ctx, r.db.Rebind(query), path, string(valueJSON), now, sessionID, stepPath, identity)
	if err != nil {
		return 0, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	return rows, nil
}

func (r *Repository) completionGeneration(ctx context.Context, tx stepTransitionTx, taskID string) (string, error) {
	var generation int64
	err := tx.QueryRowContext(ctx, r.db.Rebind(`SELECT COALESCE(MAX(id),0) FROM task_step_transitions WHERE task_id = ?`), taskID).Scan(&generation)
	return strconv.FormatInt(generation, 10), err
}
func (r *Repository) stampCompletionGeneration(ctx context.Context, tx stepTransitionTx, turn *models.Turn) error {
	generation, err := r.completionGeneration(ctx, tx, turn.TaskID)
	if err != nil {
		return err
	}
	turn.Metadata[models.TurnMetaKeyStepGenerationAtStart] = generation
	return nil
}

// CompletionTurnCurrent verifies generation under the same locks used by claims.
func (r *Repository) CompletionTurnCurrent(ctx context.Context, taskID, sessionID, stepID, turnID string) (bool, error) {
	if turnID == "" {
		return false, nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockSessionTurnWrites(ctx, tx, r.db.DriverName(), sessionID); err != nil {
		return false, err
	}
	_, current, found, err := r.readTaskStepInTx(ctx, tx, taskID)
	if err != nil || !found || current != stepID {
		return false, err
	}
	err = r.validateCompletionClaimTurn(ctx, tx, taskID, sessionID, models.PendingStepCompletionSignal{TurnID: turnID})
	return err == nil, err
}
