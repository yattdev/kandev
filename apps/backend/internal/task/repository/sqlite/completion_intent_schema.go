package sqlite

import "fmt"

// initCompletionIntentSchema creates the two additive durable record families
// for exact completion recovery. Existing signals remain metadata-only until a
// future claim captures their authoritative turn and entry identity.
func (r *Repository) initCompletionIntentSchema() error {
	_, err := r.db.ExecContext(r.migrationContext(), `
	CREATE TABLE IF NOT EXISTS session_completion_intents (
		id TEXT PRIMARY KEY,
		workspace_id TEXT NOT NULL,
		task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
		session_id TEXT NOT NULL REFERENCES task_sessions(id) ON DELETE CASCADE,
		turn_id TEXT NOT NULL REFERENCES task_session_turns(id) ON DELETE CASCADE,
		workflow_step_id TEXT NOT NULL,
		workflow_step_transition_id BIGINT NOT NULL,
		execution_id TEXT NOT NULL DEFAULT '',
		queue_incarnation_id TEXT NOT NULL DEFAULT '',
		signal_payload TEXT NOT NULL,
		signaled_at TIMESTAMP NOT NULL,
		eligible_at TIMESTAMP NOT NULL,
		state TEXT NOT NULL,
		outcome TEXT NOT NULL DEFAULT '',
		version BIGINT NOT NULL DEFAULT 1,
		lease_expires_at TIMESTAMP,
		settled_at TIMESTAMP,
		created_at TIMESTAMP NOT NULL,
		updated_at TIMESTAMP NOT NULL
	);
	CREATE UNIQUE INDEX IF NOT EXISTS idx_completion_intents_identity
		ON session_completion_intents(session_id, turn_id, workflow_step_transition_id);
	CREATE INDEX IF NOT EXISTS idx_completion_intents_due
		ON session_completion_intents(state, eligible_at, id);
	CREATE INDEX IF NOT EXISTS idx_completion_intents_task_entry
		ON session_completion_intents(task_id, workflow_step_transition_id, id);

	CREATE TABLE IF NOT EXISTS session_control_events (
		id TEXT PRIMARY KEY,
		workspace_id TEXT NOT NULL,
		actor_task_id TEXT NOT NULL,
		actor_session_id TEXT NOT NULL,
		target_task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
		target_session_id TEXT NOT NULL REFERENCES task_sessions(id) ON DELETE CASCADE,
		target_turn_id TEXT,
		completion_intent_id TEXT REFERENCES session_completion_intents(id) ON DELETE SET NULL,
		workflow_step_transition_id BIGINT,
		authority TEXT NOT NULL,
		evidence_revision TEXT NOT NULL,
		evidence_digest TEXT NOT NULL,
		outcome TEXT NOT NULL,
		reason TEXT NOT NULL,
		created_at TIMESTAMP NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_session_control_events_target
		ON session_control_events(target_session_id, created_at, id);
	CREATE INDEX IF NOT EXISTS idx_session_control_events_intent
		ON session_control_events(completion_intent_id, created_at, id);
	`)
	if err != nil {
		return fmt.Errorf("init completion intent schema: %w", err)
	}
	return nil
}
