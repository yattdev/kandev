package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/kandev/kandev/internal/task/models"
)

// SettleCompletionIntent atomically closes the captured turn, parks its
// session, terminalizes the intent, and records one immutable control event.
// It is repository-only until task 02 supplies the parent authority checks.
func (r *Repository) SettleCompletionIntent(ctx context.Context, intentID string, event models.SessionControlEvent) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin completion settlement: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	intent, state, err := r.readCompletionIntentTx(ctx, tx, intentID)
	if err != nil {
		return err
	}
	if state == models.CompletionIntentStateSettled {
		return tx.Commit()
	}
	if state != models.CompletionIntentStatePending {
		return sql.ErrNoRows
	}
	if err := r.requireCompletionIntentEntryTx(ctx, tx, intent); err != nil {
		return err
	}
	if err := lockSessionTurnWrites(ctx, tx, r.db.DriverName(), intent.sessionID); err != nil {
		return err
	}
	if err := r.applyCompletionIntentSettlementTx(ctx, tx, intentID, intent, event); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit completion settlement: %w", err)
	}
	return nil
}

func (r *Repository) applyCompletionIntentSettlementTx(ctx context.Context, tx *sqlx.Tx, intentID string, intent completionIntentIdentity, event models.SessionControlEvent) error {
	now := r.nowUTC()
	steps := []struct {
		stage string
		apply func() error
	}{
		{"turn", func() error { return r.closeCompletionIntentTurnTx(ctx, tx, intent, now) }},
		{"session", func() error { return r.parkCompletionIntentSessionTx(ctx, tx, intent, now) }},
		{"intent", func() error { return r.settleCompletionIntentTx(ctx, tx, intentID, now) }},
		{"event", func() error { return r.insertSessionControlEventTx(ctx, tx, intentID, intent, event, now) }},
	}
	for _, step := range steps {
		if err := step.apply(); err != nil {
			return err
		}
		if err := r.failCompletionSettlement(step.stage); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) failCompletionSettlement(stage string) error {
	if r.completionSettlementFailpoint == nil {
		return nil
	}
	return r.completionSettlementFailpoint(stage)
}

type completionIntentIdentity struct {
	taskID, sessionID, turnID string
	entryID                   int64
}

func (r *Repository) readCompletionIntentTx(ctx context.Context, tx *sqlx.Tx, intentID string) (completionIntentIdentity, string, error) {
	var intent completionIntentIdentity
	var state string
	err := tx.QueryRowContext(ctx, r.db.Rebind(`
		SELECT task_id, session_id, turn_id, workflow_step_transition_id
		FROM session_completion_intents WHERE id = ?
	`), intentID).Scan(&intent.taskID, &intent.sessionID, &intent.turnID, &intent.entryID)
	if err != nil {
		return completionIntentIdentity{}, "", fmt.Errorf("read completion intent: %w", err)
	}
	err = tx.QueryRowContext(ctx, r.db.Rebind(`SELECT state FROM session_completion_intents WHERE id = ?`), intentID).Scan(&state)
	if err != nil {
		return completionIntentIdentity{}, "", fmt.Errorf("read completion intent state: %w", err)
	}
	return intent, state, nil
}

func (r *Repository) requireCompletionIntentEntryTx(ctx context.Context, tx *sqlx.Tx, intent completionIntentIdentity) error {
	_, _, found, err := r.readTaskStepInTx(ctx, tx, intent.taskID)
	if err != nil || !found {
		return ErrStepCompletionStale
	}
	entry, err := r.getLatestTaskStepTransitionIDTx(ctx, tx, intent.taskID)
	if err != nil || entry != intent.entryID {
		return ErrStepCompletionStale
	}
	return nil
}

func (r *Repository) closeCompletionIntentTurnTx(ctx context.Context, tx *sqlx.Tx, intent completionIntentIdentity, now time.Time) error {
	result, err := tx.ExecContext(ctx, r.db.Rebind(`
		UPDATE task_session_turns SET completed_at = ?, updated_at = ?
		WHERE id = ? AND task_session_id = ? AND task_id = ? AND completed_at IS NULL
	`), now, now, intent.turnID, intent.sessionID, intent.taskID)
	if err != nil {
		return fmt.Errorf("close completion intent turn: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect completion intent turn: %w", err)
	}
	if count != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (r *Repository) parkCompletionIntentSessionTx(ctx context.Context, tx *sqlx.Tx, intent completionIntentIdentity, now time.Time) error {
	result, err := tx.ExecContext(ctx, r.db.Rebind(`
		UPDATE task_sessions SET state = ?, updated_at = ? WHERE id = ? AND task_id = ?
	`), models.TaskSessionStateWaitingForInput, now, intent.sessionID, intent.taskID)
	if err != nil {
		return fmt.Errorf("park completion intent session: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect completion intent session: %w", err)
	}
	if count != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (r *Repository) settleCompletionIntentTx(ctx context.Context, tx *sqlx.Tx, intentID string, now time.Time) error {
	result, err := tx.ExecContext(ctx, r.db.Rebind(`
		UPDATE session_completion_intents
		SET state = ?, outcome = ?, settled_at = ?, updated_at = ?, version = version + 1
		WHERE id = ? AND state = ?
	`), models.CompletionIntentStateSettled, "settled", now, now, intentID, models.CompletionIntentStatePending)
	if err != nil {
		return fmt.Errorf("settle completion intent: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect completion intent settlement: %w", err)
	}
	if count != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (r *Repository) insertSessionControlEventTx(ctx context.Context, tx *sqlx.Tx, intentID string, intent completionIntentIdentity, event models.SessionControlEvent, now time.Time) error {
	if event.ID == "" {
		event.ID = uuid.NewString()
	}
	_, err := tx.ExecContext(ctx, r.db.Rebind(`
		INSERT INTO session_control_events (
			id, workspace_id, actor_task_id, actor_session_id, target_task_id,
			target_session_id, target_turn_id, completion_intent_id, authority,
			evidence_revision, evidence_digest, outcome, reason, created_at
		)
		SELECT ?, workspace_id, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
		FROM session_completion_intents WHERE session_id = ? AND turn_id = ?
	`), event.ID, event.ActorTaskID, event.ActorSessionID, intent.taskID, intent.sessionID,
		intent.turnID, intentID, event.Authority, event.EvidenceRevision,
		event.EvidenceDigest, event.Outcome, event.Reason, now, intent.sessionID, intent.turnID)
	if err != nil {
		return fmt.Errorf("insert completion control event: %w", err)
	}
	return nil
}
