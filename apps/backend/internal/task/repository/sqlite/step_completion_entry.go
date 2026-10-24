package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/kandev/kandev/internal/task/models"
)

// ErrStepCompletionStale means the calling turn no longer owns the current entry.
var ErrStepCompletionStale = errors.New("workflow entry changed before signal was recorded")

// ClaimStepCompletionSignal serializes entry validation and first-signal-wins
// against task moves. A nil entry is reserved for the no-turn compatibility path.
func (r *Repository) ClaimStepCompletionSignal(ctx context.Context, taskID, sessionID string, signal models.PendingStepCompletionSignal) (models.PendingStepCompletionSignal, bool, error) {
	return r.ClaimStepCompletionSignalForTurn(ctx, taskID, sessionID, "", signal)
}

// ClaimStepCompletionSignalForTurn claims a signal and its recovery intent in
// one transaction. An empty turnID retains the legacy metadata-only path; it
// must never infer a recovery-eligible intent from a newest-turn lookup.
func (r *Repository) ClaimStepCompletionSignalForTurn(ctx context.Context, taskID, sessionID, turnID string, signal models.PendingStepCompletionSignal) (models.PendingStepCompletionSignal, bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return signal, false, err
	}
	defer func() { _ = tx.Rollback() }()
	entry, err := r.lockStepCompletionEntryTx(ctx, tx, taskID, signal)
	if err != nil {
		return signal, false, err
	}
	stepID := signal.StepID
	signal.WorkflowStepTransitionID = &entry
	var owner, metadata string
	err = tx.QueryRowContext(ctx, r.db.Rebind(`SELECT task_id,COALESCE(NULLIF(metadata,''),'{}') FROM task_sessions WHERE id=?`), sessionID).Scan(&owner, &metadata)
	if err != nil {
		return signal, false, err
	}
	if owner != taskID {
		return signal, false, ErrStepCompletionStale
	}
	var bag map[string]interface{}
	if err = json.Unmarshal([]byte(metadata), &bag); err != nil {
		return signal, false, fmt.Errorf("decode completion metadata: %w", err)
	}
	if stored, ok := models.LoadPendingStepSignal(bag); ok && stored.WorkflowStepTransitionID != nil && stored.MatchesWorkflowEntry(stepID, entry) {
		return stored, false, tx.Commit()
	}
	raw, err := json.Marshal(signal)
	if err != nil {
		return signal, false, err
	}
	result, err := tx.ExecContext(ctx, r.db.Rebind(metadataKeyUpdateQuery("task_sessions", r.db.DriverName())), metadataKeyUpdateArgs(r.db.DriverName(), models.SessionMetaKeyPendingStepCompletion, string(raw), r.nowUTC(), sessionID)...)
	if err != nil {
		return signal, false, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return signal, false, err
	}
	if count != 1 {
		return signal, false, sql.ErrNoRows
	}
	if turnID != "" {
		if err := r.insertCompletionIntentTx(ctx, tx, taskID, sessionID, turnID, signal); err != nil {
			return signal, false, err
		}
	}
	return signal, true, tx.Commit()
}

func (r *Repository) insertCompletionIntentTx(ctx context.Context, tx *sql.Tx, taskID, sessionID, turnID string, signal models.PendingStepCompletionSignal) error {
	var workspaceID, turnMetadata string
	err := tx.QueryRowContext(ctx, r.db.Rebind(`
		SELECT t.workspace_id, COALESCE(NULLIF(turn.metadata, ''), '{}')
		FROM tasks t
		JOIN task_sessions s ON s.id = ? AND s.task_id = t.id
		JOIN task_session_turns turn ON turn.id = ? AND turn.task_session_id = s.id AND turn.task_id = t.id
		WHERE t.id = ?
	`), sessionID, turnID, taskID).Scan(&workspaceID, &turnMetadata)
	if err != nil {
		return fmt.Errorf("read completion intent source: %w", err)
	}
	var metadata map[string]interface{}
	if err := json.Unmarshal([]byte(turnMetadata), &metadata); err != nil {
		return fmt.Errorf("decode completion intent turn metadata: %w", err)
	}
	if models.StringFromAny(metadata[models.TurnMetaKeyWorkflowStepIDAtStart]) != signal.StepID {
		return ErrStepCompletionStale
	}
	entry, ok := models.WorkflowEntryIDFromAny(metadata[models.TurnMetaKeyWorkflowStepTransitionIDAtStart])
	if !ok || signal.WorkflowStepTransitionID == nil || entry != *signal.WorkflowStepTransitionID {
		return ErrStepCompletionStale
	}
	payload, err := json.Marshal(signal)
	if err != nil {
		return fmt.Errorf("encode completion intent signal: %w", err)
	}
	now := r.nowUTC()
	if signal.SignaledAt.IsZero() {
		signal.SignaledAt = now
	}
	_, err = tx.ExecContext(ctx, r.db.Rebind(`
		INSERT INTO session_completion_intents (
			id, workspace_id, task_id, session_id, turn_id, workflow_step_id,
			workflow_step_transition_id, execution_id, queue_incarnation_id,
			signal_payload, signaled_at, eligible_at, state, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`), uuid.NewString(), workspaceID, taskID, sessionID, turnID, signal.StepID,
		entry, "", "", string(payload), signal.SignaledAt,
		signal.SignaledAt.Add(10*time.Minute), models.CompletionIntentStatePending, now, now)
	if err != nil {
		return fmt.Errorf("insert completion intent: %w", err)
	}
	return nil
}

func (r *Repository) lockStepCompletionEntryTx(ctx context.Context, tx *sql.Tx, taskID string, signal models.PendingStepCompletionSignal) (int64, error) {
	_, stepID, found, err := r.readTaskStepInTx(ctx, tx, taskID)
	if err != nil {
		return 0, err
	}
	if !found || stepID != signal.StepID {
		return 0, ErrStepCompletionStale
	}
	entry, err := r.getLatestTaskStepTransitionIDTx(ctx, tx, taskID)
	if err != nil {
		return 0, err
	}
	if signal.WorkflowStepTransitionID != nil && *signal.WorkflowStepTransitionID != entry {
		return 0, ErrStepCompletionStale
	}
	return entry, nil
}

// rebaseCompletionHandoff overlays only the carry slot owned by this completion.
// All unrelated task metadata comes from the locked current row.
func rebaseCompletionHandoff(ctx context.Context, requested, current *models.Task, stepID string) {
	if !models.WorkflowStepCompletionOwnsHandoff(ctx, requested.ID, stepID) {
		return
	}
	if current.Metadata == nil {
		current.Metadata = make(map[string]interface{})
	}
	if carry, present := requested.Metadata[models.MetaKeyStepHandoffCarry]; present {
		current.Metadata[models.MetaKeyStepHandoffCarry] = carry
	} else {
		delete(current.Metadata, models.MetaKeyStepHandoffCarry)
	}
}
