package sqlite

import (
	"context"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/kandev/kandev/internal/agentctl/types/streams"
	"github.com/kandev/kandev/internal/db/dialect"
	"github.com/kandev/kandev/internal/task/ownanswer"
)

type approvalCommitFence struct {
	Incarnation       string
	SessionUpdatedAt  time.Time
	ExecutionID       string
	ExecutorUpdatedAt time.Time
}

// Row locks remain held through commit. Timestamps fence re-registration of
// the same identity; the incarnation fences replacement of the session.
func (r *Repository) captureApprovalCommitFence(ctx context.Context, tx *sqlx.Tx, b ownanswer.OwnAnswerBinding) (*approvalCommitFence, error) {
	f := &approvalCommitFence{}
	err := tx.QueryRowContext(ctx, r.db.Rebind(`SELECT queue_incarnation_id,updated_at FROM task_sessions WHERE id=? AND task_id=?`), b.SessionID, b.TaskID).Scan(&f.Incarnation, &f.SessionUpdatedAt)
	if err != nil || f.Incarnation == "" {
		return nil, errApprovalLink
	}
	e, ok := streams.MCPExecutionContextFromContext(ctx)
	if !ok {
		return f, nil
	}
	query := `SELECT agent_execution_id,updated_at FROM executors_running WHERE task_id=? AND session_id=? AND status IN ('starting','running','ready')`
	if dialect.IsPostgres(r.db.DriverName()) {
		query += forUpdateClause
	}
	err = tx.QueryRowContext(ctx, r.db.Rebind(query), b.TaskID, b.SessionID).Scan(&f.ExecutionID, &f.ExecutorUpdatedAt)
	if err != nil || f.ExecutionID != e.ExecutionID {
		return nil, errApprovalLink
	}
	return f, nil
}

func (r *Repository) commitApprovalLink(ctx context.Context, tx *sqlx.Tx, b ownanswer.OwnAnswerBinding, before *approvalCommitFence, g *ownanswer.ApprovalLinkGrant) error {
	if err := r.guardOwnAnswerScope(ctx, tx, b); err != nil {
		return err
	}
	after, err := r.captureApprovalCommitFence(ctx, tx, b)
	if err != nil {
		return err
	}
	if before.Incarnation != after.Incarnation || before.ExecutionID != after.ExecutionID || !before.SessionUpdatedAt.Equal(after.SessionUpdatedAt) || !before.ExecutorUpdatedAt.Equal(after.ExecutorUpdatedAt) {
		return errApprovalLink
	}
	if err = r.checkApprovalAuthority(ctx, tx, b, g); err != nil {
		return err
	}
	return tx.Commit()
}
