package sqlite

func (r *Repository) initApprovalLinkSchema() error {
	_, err := r.db.ExecContext(r.migrationContext(), `CREATE TABLE IF NOT EXISTS own_answer_approval_link_grants (
		id TEXT PRIMARY KEY,
		task_id TEXT NOT NULL,
		session_id TEXT NOT NULL,
		question_message_id TEXT NOT NULL,
		grant_json TEXT NOT NULL,
		state TEXT NOT NULL CHECK (state IN ('issued','consumed','revoked')),
		operation_id TEXT NOT NULL DEFAULT '',
		UNIQUE(task_id, question_message_id),
		FOREIGN KEY(task_id) REFERENCES tasks(id) ON DELETE CASCADE,
		FOREIGN KEY(session_id) REFERENCES task_sessions(id) ON DELETE CASCADE
	)`)
	return err
}
