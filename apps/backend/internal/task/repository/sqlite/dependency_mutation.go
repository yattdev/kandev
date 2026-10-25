package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/kandev/kandev/internal/common/taskdependencies"
	"github.com/kandev/kandev/internal/db/dialect"
	"github.com/kandev/kandev/internal/task/dependencywrite"
)

const (
	dependencyAdd    = "add"
	dependencyRemove = "remove"
)

// MutateDependency keeps an edge, both endpoint revisions, and its success
// audit in one database transaction. The caller supplies an authorization
// callback that checks the attested principal within the transaction.
func (r *Repository) MutateDependency(
	ctx context.Context, request dependencywrite.DependencyMutationRequest,
	authorize dependencywrite.DependencyMutationAuthorizer,
) (dependencywrite.DependencyMutationResult, error) {
	if !validDependencyMutationRequest(request, authorize) {
		return dependencywrite.DependencyMutationResult{}, errors.New("invalid dependency mutation")
	}
	unlock := taskdependencies.AcquireMutationLock()
	defer unlock()
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return dependencywrite.DependencyMutationResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := r.lockDependencyWorkspace(ctx, tx, request.WorkspaceID); err != nil {
		return dependencywrite.DependencyMutationResult{}, err
	}
	if err := authorize(ctx, tx); err != nil {
		return r.commitDependencyDenial(ctx, tx, request, "scope_denied", err)
	}
	result, err := r.mutateDependencyInTx(ctx, tx, request)
	if errors.Is(err, dependencywrite.ErrCycle) {
		return r.commitDependencyDenial(ctx, tx, request, "cycle", err)
	}
	if err != nil {
		return dependencywrite.DependencyMutationResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return dependencywrite.DependencyMutationResult{}, err
	}
	return result, nil
}

// RecordDependencyDenial stores a rejected request without exposing its target
// identifiers. It is used when a guard rejects before graph authorization.
func (r *Repository) RecordDependencyDenial(ctx context.Context,
	request dependencywrite.DependencyMutationRequest, reason string) error {
	if request.WorkspaceID == "" || request.CorrelationID == "" || request.OriginClass == "" ||
		(request.Operation != dependencyAdd && request.Operation != dependencyRemove) || reason == "" {
		return errors.New("invalid dependency denial")
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := r.recordDependencyMutationDenial(ctx, tx, request, reason); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *Repository) commitDependencyDenial(
	ctx context.Context, tx *sqlx.Tx, request dependencywrite.DependencyMutationRequest,
	reason string, denial error,
) (dependencywrite.DependencyMutationResult, error) {
	if err := r.recordDependencyMutationDenial(ctx, tx, request, reason); err != nil {
		return dependencywrite.DependencyMutationResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return dependencywrite.DependencyMutationResult{}, err
	}
	return dependencywrite.DependencyMutationResult{}, denial
}

func validDependencyMutationRequest(
	request dependencywrite.DependencyMutationRequest, authorize dependencywrite.DependencyMutationAuthorizer,
) bool {
	return authorize != nil && request.WorkspaceID != "" && request.DependentTaskID != "" &&
		request.PredecessorTaskID != "" && request.DependentTaskID != request.PredecessorTaskID &&
		(request.Operation == dependencyAdd || request.Operation == dependencyRemove) &&
		request.CorrelationID != "" && request.OriginClass != ""
}

func (r *Repository) recordDependencyMutationDenial(
	ctx context.Context, tx *sqlx.Tx, request dependencywrite.DependencyMutationRequest, reason string,
) error {
	dependentPresent, predecessorPresent := 0, 0
	if request.DependentTaskID != "" {
		dependentPresent = 1
	}
	if request.PredecessorTaskID != "" {
		predecessorPresent = 1
	}
	_, err := tx.ExecContext(ctx, r.db.Rebind(`INSERT INTO task_dependency_mutation_audit
		(event_id, correlation_id, created_at, operation, origin_class, outcome, reason_code, changed,
		caller_task_id, caller_session_id, caller_execution_id, caller_workspace_id, caller_user_id,
		dependent_id_present, predecessor_id_present)
		VALUES (?, ?, ?, ?, ?, 'denied', ?, 0, ?, ?, ?, ?, ?, ?, ?)`),
		uuid.NewString(), request.CorrelationID, time.Now().UTC(), request.Operation, request.OriginClass,
		reason, request.CallerTaskID, request.CallerSessionID, request.CallerExecutionID,
		request.WorkspaceID, request.CallerUserID, dependentPresent, predecessorPresent)
	return err
}

func (r *Repository) lockDependencyWorkspace(ctx context.Context, tx *sqlx.Tx, workspaceID string) error {
	query := `SELECT id FROM workspaces WHERE id = ?`
	if dialect.IsPostgres(r.db.DriverName()) {
		query += forUpdateClause
	} else {
		// SQLite's first write obtains its writer lock before scope and graph reads.
		if _, err := tx.ExecContext(ctx, r.db.Rebind(`UPDATE workspaces SET id = id WHERE id = ?`), workspaceID); err != nil {
			return err
		}
	}
	var storedID string
	if err := tx.QueryRowContext(ctx, r.db.Rebind(query), workspaceID).Scan(&storedID); err != nil {
		return err
	}
	return nil
}

func (r *Repository) mutateDependencyInTx(
	ctx context.Context, tx *sqlx.Tx, request dependencywrite.DependencyMutationRequest,
) (dependencywrite.DependencyMutationResult, error) {
	ids := []string{request.DependentTaskID, request.PredecessorTaskID}
	sort.Strings(ids)
	revisions := make(map[string]int64, 2)
	for _, id := range ids {
		var workspaceID string
		var revision int64
		query := `SELECT workspace_id, dependency_revision FROM tasks WHERE id = ?`
		if dialect.IsPostgres(r.db.DriverName()) {
			query += forUpdateClause
		}
		if err := tx.QueryRowContext(ctx, r.db.Rebind(query), id).Scan(&workspaceID, &revision); err != nil {
			return dependencywrite.DependencyMutationResult{}, err
		}
		if workspaceID != request.WorkspaceID {
			return dependencywrite.DependencyMutationResult{}, sql.ErrNoRows
		}
		revisions[id] = revision
	}
	var exists int
	err := tx.QueryRowContext(ctx, r.db.Rebind(`SELECT 1 FROM task_blockers WHERE task_id = ? AND blocker_task_id = ?`),
		request.DependentTaskID, request.PredecessorTaskID).Scan(&exists)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return dependencywrite.DependencyMutationResult{}, err
	}
	changed := (request.Operation == dependencyAdd && exists == 0) || (request.Operation == dependencyRemove && exists == 1)
	if changed {
		if err := r.writeDependencyChangeInTx(ctx, tx, request); err != nil {
			return dependencywrite.DependencyMutationResult{}, err
		}
		for _, id := range ids {
			if _, err := tx.ExecContext(ctx, r.db.Rebind(`UPDATE tasks SET dependency_revision = dependency_revision + 1 WHERE id = ?`), id); err != nil {
				return dependencywrite.DependencyMutationResult{}, err
			}
			revisions[id]++
		}
	}
	result := dependencywrite.DependencyMutationResult{
		Changed: changed, DependentRevision: revisions[request.DependentTaskID],
		PredecessorRevision: revisions[request.PredecessorTaskID],
	}
	if err := r.recordDependencyMutationSuccess(ctx, tx, request, result); err != nil {
		return dependencywrite.DependencyMutationResult{}, err
	}
	return result, nil
}

func (r *Repository) writeDependencyChangeInTx(
	ctx context.Context, tx *sqlx.Tx, request dependencywrite.DependencyMutationRequest,
) error {
	if request.Operation == dependencyAdd {
		if err := checkDependencyCycleInTx(ctx, tx, r.db.Rebind, request.DependentTaskID, request.PredecessorTaskID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, r.db.Rebind(`INSERT INTO task_blockers (task_id, blocker_task_id, created_at) VALUES (?, ?, ?)`),
			request.DependentTaskID, request.PredecessorTaskID, time.Now().UTC())
		return err
	}
	_, err := tx.ExecContext(ctx, r.db.Rebind(`DELETE FROM task_blockers WHERE task_id = ? AND blocker_task_id = ?`),
		request.DependentTaskID, request.PredecessorTaskID)
	return err
}

func checkDependencyCycleInTx(
	ctx context.Context, tx *sqlx.Tx, rebind func(string) string, dependentID, predecessorID string,
) error {
	seen := map[string]bool{}
	queue := []string{predecessorID}
	for len(queue) > 0 {
		if len(seen) > 1000 {
			return errors.New("dependency cycle walk limit reached")
		}
		current := queue[0]
		queue = queue[1:]
		if current == dependentID {
			return dependencywrite.ErrCycle
		}
		if seen[current] {
			continue
		}
		seen[current] = true
		rows, err := tx.QueryContext(ctx, rebind(`SELECT blocker_task_id FROM task_blockers WHERE task_id = ?`), current)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return err
			}
			queue = append(queue, id)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) recordDependencyMutationSuccess(
	ctx context.Context, tx *sqlx.Tx, request dependencywrite.DependencyMutationRequest,
	result dependencywrite.DependencyMutationResult,
) error {
	reason := "edge_added"
	if request.Operation == dependencyRemove {
		reason = "edge_removed"
	}
	if !result.Changed {
		reason = "no_change"
	}
	changed := 0
	if result.Changed {
		changed = 1
	}
	_, err := tx.ExecContext(ctx, r.db.Rebind(`INSERT INTO task_dependency_mutation_audit
		(event_id, correlation_id, created_at, operation, origin_class, outcome, reason_code, changed,
		caller_task_id, caller_session_id, caller_execution_id, caller_workspace_id, caller_user_id,
		dependent_task_id, predecessor_task_id, dependent_id_present, predecessor_id_present,
		dependent_id_canonical, predecessor_id_canonical, dependent_revision, predecessor_revision)
		VALUES (?, ?, ?, ?, ?, 'succeeded', ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, 1, 1, 1, ?, ?)`),
		uuid.NewString(), request.CorrelationID, time.Now().UTC(), request.Operation, request.OriginClass,
		reason, changed, request.CallerTaskID, request.CallerSessionID, request.CallerExecutionID,
		request.WorkspaceID, request.CallerUserID, request.DependentTaskID, request.PredecessorTaskID,
		result.DependentRevision, result.PredecessorRevision)
	if err != nil {
		return fmt.Errorf("record dependency mutation: %w", err)
	}
	return nil
}
