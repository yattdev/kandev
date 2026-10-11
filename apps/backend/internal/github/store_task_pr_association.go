package github

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/kandev/kandev/internal/db/dialect"
)

// AssociateTaskPR records a provider observation after an ordinary absent or detached
// precheck. Recheck inside the shared write fence rather than overwriting a row
// that another request installed while this caller waited for the transaction.
// ReplaceTaskPR remains the explicit upsert primitive for deliberate replacements.
func (s *Store) AssociateTaskPR(ctx context.Context, tp *TaskPR, status *PRStatus, restoreDetached bool) (*TaskPR, bool, error) {
	tx, err := s.beginPRMutation(ctx)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback() }()
	var existing TaskPR
	query := `SELECT ` + taskPRColumns + ` FROM github_task_prs WHERE task_id=? AND repository_id=? AND pr_number=?`
	if dialect.IsPostgres(s.db.DriverName()) {
		query += ` FOR UPDATE`
	}
	err = tx.GetContext(ctx, &existing, tx.Rebind(query), tp.TaskID, tp.RepositoryID, tp.PRNumber)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	changed := false
	var saved *TaskPR
	if errors.Is(err, sql.ErrNoRows) {
		saved, err = replaceTaskPRTx(ctx, tx, tp, status)
		changed = true
	} else {
		if existing.DetachedAt == nil && !strings.EqualFold(strings.TrimSuffix(existing.PRURL, "/"), strings.TrimSuffix(tp.PRURL, "/")) {
			return nil, false, errors.New("canonical PR slot changed during association")
		}
		saved = &existing
		if existing.DetachedAt != nil && restoreDetached {
			saved, err = restoreTaskPRTx(ctx, tx, tp.TaskID, tp.RepositoryID, status)
			changed = true
		} else {
			err = hydrateTaskPRWorkflowAttention(saved)
		}
	}
	if err != nil {
		return nil, false, err
	}
	if err = tx.Commit(); err != nil {
		return nil, false, err
	}
	return saved, changed, nil
}
