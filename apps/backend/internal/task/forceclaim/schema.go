package forceclaim

import (
	"context"

	"github.com/jmoiron/sqlx"
)

// EnsureSchema installs the force-removal claim and receipt tables needed by
// every task-owned writer that must reject a retained task.
func EnsureSchema(ctx context.Context, db *sqlx.DB) error {
	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS task_force_removal_claims (
			task_id TEXT PRIMARY KEY,
			workspace_id TEXT NOT NULL,
			task_generation TIMESTAMP NOT NULL,
			admission_generation TEXT NOT NULL,
			operation_id TEXT NOT NULL UNIQUE,
			request_digest TEXT NOT NULL,
			preview_digest TEXT NOT NULL,
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL
		);
		CREATE TABLE IF NOT EXISTS task_force_removal_receipts (
			operation_id TEXT NOT NULL,
			ordinal INTEGER NOT NULL,
			predicate TEXT NOT NULL,
			status TEXT NOT NULL,
			reason_code TEXT NOT NULL,
			resource_id TEXT NOT NULL,
			observed_generation TEXT NOT NULL,
			evidence_digest TEXT NOT NULL,
			created_at TIMESTAMP NOT NULL,
			PRIMARY KEY (operation_id, ordinal),
			FOREIGN KEY (operation_id) REFERENCES task_force_removal_claims(operation_id)
		);
		CREATE UNIQUE INDEX IF NOT EXISTS idx_task_force_removal_receipts_operation_predicate
			ON task_force_removal_receipts(operation_id, predicate)`)
	return err
}
