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

// ReplaceDependencySet applies the full predecessor set in one graph and audit
// transaction. Removing obsolete edges first avoids transient false cycles.
func (r *Repository) ReplaceDependencySet(ctx context.Context,
	request dependencywrite.DependencyMutationRequest, desired []string,
	authorize dependencywrite.DependencyMutationAuthorizer) (dependencywrite.DependencyBatchResult, error) {
	if request.WorkspaceID == "" || request.DependentTaskID == "" || request.CorrelationID == "" ||
		request.OriginClass == "" || authorize == nil {
		return dependencywrite.DependencyBatchResult{}, errors.New("invalid dependency replacement")
	}
	unlock := taskdependencies.AcquireMutationLock()
	defer unlock()
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return dependencywrite.DependencyBatchResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := r.lockDependencyWorkspace(ctx, tx, request.WorkspaceID); err != nil {
		return dependencywrite.DependencyBatchResult{}, err
	}
	if err := authorize(ctx, tx); err != nil {
		return dependencywrite.DependencyBatchResult{}, err
	}
	result, err := r.replaceDependencySetInTx(ctx, tx, request, desired)
	if err != nil {
		return dependencywrite.DependencyBatchResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return dependencywrite.DependencyBatchResult{}, err
	}
	return result, nil
}

func (r *Repository) replaceDependencySetInTx(ctx context.Context, tx *sqlx.Tx,
	request dependencywrite.DependencyMutationRequest, desired []string) (dependencywrite.DependencyBatchResult, error) {
	ids := append([]string{request.DependentTaskID}, desired...)
	sort.Strings(ids)
	for i, id := range ids {
		if id == "" || (i > 0 && id == ids[i-1]) {
			return dependencywrite.DependencyBatchResult{}, errors.New("invalid dependency replacement set")
		}
	}
	if _, err := r.dependencyRevisionsInTx(ctx, tx, request.WorkspaceID, ids); err != nil {
		return dependencywrite.DependencyBatchResult{}, err
	}
	var existing []string
	if err := tx.SelectContext(ctx, &existing, r.db.Rebind(
		`SELECT blocker_task_id FROM task_blockers WHERE task_id = ? ORDER BY blocker_task_id`),
		request.DependentTaskID); err != nil {
		return dependencywrite.DependencyBatchResult{}, err
	}
	changed, err := r.applyDependencySetDiffInTx(ctx, tx, request, existing, desired)
	if err != nil {
		return dependencywrite.DependencyBatchResult{}, err
	}
	return r.dependencyBatchSnapshotsInTx(ctx, tx, request.DependentTaskID, changed)
}

func (r *Repository) applyDependencySetDiffInTx(ctx context.Context, tx *sqlx.Tx,
	request dependencywrite.DependencyMutationRequest, existing, desired []string) (map[string]bool, error) {
	desiredSet := make(map[string]bool, len(desired))
	for _, id := range desired {
		desiredSet[id] = true
	}
	existingSet := make(map[string]bool, len(existing))
	changed := map[string]bool{}
	for _, id := range existing {
		existingSet[id] = true
		if !desiredSet[id] {
			request.Operation, request.PredecessorTaskID = dependencyRemove, id
			if _, err := r.mutateDependencyInTx(ctx, tx, request); err != nil {
				return nil, err
			}
			changed[id] = true
		}
	}
	for _, id := range desired {
		if existingSet[id] {
			continue
		}
		request.Operation, request.PredecessorTaskID = dependencyAdd, id
		if _, err := r.mutateDependencyInTx(ctx, tx, request); err != nil {
			return nil, err
		}
		changed[id] = true
	}
	return changed, nil
}

func (r *Repository) dependencyBatchSnapshotsInTx(ctx context.Context, tx *sqlx.Tx,
	dependentID string, changed map[string]bool) (dependencywrite.DependencyBatchResult, error) {
	result := dependencywrite.DependencyBatchResult{Changed: []dependencywrite.DependencySnapshot{}}
	if len(changed) > 0 {
		if dependentID != "" {
			changed[dependentID] = true
		}
		changedIDs := make([]string, 0, len(changed))
		for id := range changed {
			changedIDs = append(changedIDs, id)
		}
		sort.Strings(changedIDs)
		for _, id := range changedIDs {
			var revision int64
			if err := tx.QueryRowContext(ctx, r.db.Rebind(
				`SELECT dependency_revision FROM tasks WHERE id = ?`), id).Scan(&revision); err != nil {
				return dependencywrite.DependencyBatchResult{}, err
			}
			snapshot, err := r.dependencySnapshotInTx(ctx, tx, id, revision)
			if err != nil {
				return dependencywrite.DependencyBatchResult{}, err
			}
			result.Changed = append(result.Changed, snapshot)
		}
	}
	return result, nil
}

func (r *Repository) deleteTaskDependenciesInTx(ctx context.Context, tx *sqlx.Tx,
	workspaceID, taskID string) ([]dependencywrite.DependencySnapshot, error) {
	var tableExists bool
	query := `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'task_blockers')`
	if dialect.IsPostgres(r.db.DriverName()) {
		query = `SELECT to_regclass('task_blockers') IS NOT NULL`
	}
	if err := tx.QueryRowContext(ctx, query).Scan(&tableExists); err != nil {
		return nil, err
	}
	if !tableExists {
		return []dependencywrite.DependencySnapshot{}, nil
	}
	type edge struct {
		Dependent   string `db:"task_id"`
		Predecessor string `db:"blocker_task_id"`
	}
	var edges []edge
	if err := tx.SelectContext(ctx, &edges, r.db.Rebind(
		`SELECT task_id, blocker_task_id FROM task_blockers WHERE task_id = ? OR blocker_task_id = ? ORDER BY task_id, blocker_task_id`),
		taskID, taskID); err != nil {
		return nil, err
	}
	changed := map[string]bool{}
	for _, item := range edges {
		request := dependencywrite.DependencyMutationRequest{
			Operation: dependencyRemove, WorkspaceID: workspaceID,
			DependentTaskID: item.Dependent, PredecessorTaskID: item.Predecessor,
			OriginClass: "task_delete", CorrelationID: taskID,
		}
		if _, err := r.mutateDependencyInTx(ctx, tx, request); err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				return nil, err
			}
			if err := r.removeLegacyDependencyEdgeInTx(ctx, tx, request); err != nil {
				return nil, err
			}
			continue
		}
		if item.Dependent != taskID {
			changed[item.Dependent] = true
		}
		if item.Predecessor != taskID {
			changed[item.Predecessor] = true
		}
	}
	result, err := r.dependencyBatchSnapshotsInTx(ctx, tx, "", changed)
	if err != nil {
		return nil, err
	}
	return result.Changed, nil
}

// PruneDanglingDependency removes a legacy edge only if its predecessor is
// still absent at the write boundary. The surviving task advances once.
func (r *Repository) PruneDanglingDependency(ctx context.Context,
	request dependencywrite.DependencyMutationRequest) (dependencywrite.DependencySnapshot, bool, error) {
	if request.WorkspaceID == "" || request.DependentTaskID == "" || request.PredecessorTaskID == "" ||
		request.DependentTaskID == request.PredecessorTaskID || request.CorrelationID == "" {
		return dependencywrite.DependencySnapshot{}, false, errors.New("invalid dangling dependency prune")
	}
	unlock := taskdependencies.AcquireMutationLock()
	defer unlock()
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return dependencywrite.DependencySnapshot{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := r.lockDependencyWorkspace(ctx, tx, request.WorkspaceID); err != nil {
		return dependencywrite.DependencySnapshot{}, false, err
	}
	snapshot, changed, err := r.pruneDanglingDependencyInTx(ctx, tx, request)
	if err != nil || !changed {
		return snapshot, changed, err
	}
	if err := tx.Commit(); err != nil {
		return dependencywrite.DependencySnapshot{}, false, err
	}
	return snapshot, true, nil
}

func (r *Repository) pruneDanglingDependencyInTx(ctx context.Context, tx *sqlx.Tx,
	request dependencywrite.DependencyMutationRequest) (dependencywrite.DependencySnapshot, bool, error) {
	revisions, err := r.dependencyRevisionsInTx(ctx, tx, request.WorkspaceID, []string{request.DependentTaskID})
	if err != nil {
		return dependencywrite.DependencySnapshot{}, false, err
	}
	var present int
	err = tx.QueryRowContext(ctx, r.db.Rebind(`SELECT 1 FROM tasks WHERE id = ?`), request.PredecessorTaskID).Scan(&present)
	if err == nil {
		return dependencywrite.DependencySnapshot{}, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return dependencywrite.DependencySnapshot{}, false, err
	}
	result, err := tx.ExecContext(ctx, r.db.Rebind(
		`DELETE FROM task_blockers WHERE task_id = ? AND blocker_task_id = ?`),
		request.DependentTaskID, request.PredecessorTaskID)
	if err != nil {
		return dependencywrite.DependencySnapshot{}, false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return dependencywrite.DependencySnapshot{}, false, err
	}
	if affected == 0 {
		return dependencywrite.DependencySnapshot{}, false, nil
	}
	if err := r.incrementDependencyRevisionsInTx(ctx, tx,
		[]string{request.DependentTaskID}, revisions); err != nil {
		return dependencywrite.DependencySnapshot{}, false, err
	}
	snapshot, err := r.dependencySnapshotInTx(ctx, tx, request.DependentTaskID,
		revisions[request.DependentTaskID])
	if err != nil {
		return dependencywrite.DependencySnapshot{}, false, err
	}
	if _, err := tx.ExecContext(ctx, r.db.Rebind(`INSERT INTO task_dependency_mutation_audit
		(event_id, correlation_id, created_at, operation, origin_class, outcome, reason_code, changed,
		caller_workspace_id, dependent_task_id, predecessor_task_id, dependent_id_present, predecessor_id_present,
		dependent_id_canonical, predecessor_id_canonical, dependent_revision)
		VALUES (?, ?, ?, 'remove', 'task_service', 'succeeded', 'dangling_pruned', 1, ?, ?, ?, 1, 1, 1, 0, ?)`),
		uuid.NewString(), request.CorrelationID, time.Now().UTC(), request.WorkspaceID,
		request.DependentTaskID, request.PredecessorTaskID, snapshot.Revision); err != nil {
		return dependencywrite.DependencySnapshot{}, false, err
	}
	return snapshot, true, nil
}

func (r *Repository) removeLegacyDependencyEdgeInTx(ctx context.Context, tx *sqlx.Tx,
	request dependencywrite.DependencyMutationRequest) error {
	if _, err := tx.ExecContext(ctx, r.db.Rebind(
		`DELETE FROM task_blockers WHERE task_id = ? AND blocker_task_id = ?`),
		request.DependentTaskID, request.PredecessorTaskID); err != nil {
		return err
	}
	canonical := func(id string) (int, error) {
		var workspaceID string
		err := tx.QueryRowContext(ctx, r.db.Rebind(`SELECT workspace_id FROM tasks WHERE id = ?`), id).Scan(&workspaceID)
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		if err != nil {
			return 0, err
		}
		if workspaceID == request.WorkspaceID {
			return 1, nil
		}
		return 0, nil
	}
	dependentCanonical, err := canonical(request.DependentTaskID)
	if err != nil {
		return err
	}
	predecessorCanonical, err := canonical(request.PredecessorTaskID)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, r.db.Rebind(`INSERT INTO task_dependency_mutation_audit
		(event_id, correlation_id, created_at, operation, origin_class, outcome, reason_code, changed,
		caller_workspace_id, dependent_task_id, predecessor_task_id, dependent_id_present, predecessor_id_present,
		dependent_id_canonical, predecessor_id_canonical)
		VALUES (?, ?, ?, 'remove', 'task_delete', 'succeeded', 'legacy_cleanup', 1, ?, ?, ?, 1, 1, ?, ?)`),
		uuid.NewString(), request.CorrelationID, time.Now().UTC(), request.WorkspaceID,
		request.DependentTaskID, request.PredecessorTaskID, dependentCanonical, predecessorCanonical)
	return err
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
	if request.DependentIDPresent {
		dependentPresent = 1
	}
	if request.PredecessorIDPresent {
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

// LockDependencyWorkspaceWithinTx lets a caller with additional compare-and-
// swap checks enter the same workspace-first graph serialization order.
func (r *Repository) LockDependencyWorkspaceWithinTx(ctx context.Context, tx *sqlx.Tx, workspaceID string) error {
	return r.lockDependencyWorkspace(ctx, tx, workspaceID)
}

// MutateDependencyWithinTx joins an already-locked caller transaction while
// retaining the shared edge, revision, snapshot, and audit write semantics.
func (r *Repository) MutateDependencyWithinTx(ctx context.Context, tx *sqlx.Tx,
	request dependencywrite.DependencyMutationRequest) (dependencywrite.DependencyMutationResult, error) {
	if !validDependencyMutationRequest(request, func(context.Context, *sqlx.Tx) error { return nil }) {
		return dependencywrite.DependencyMutationResult{}, errors.New("invalid dependency mutation")
	}
	return r.mutateDependencyInTx(ctx, tx, request)
}

func (r *Repository) mutateDependencyInTx(
	ctx context.Context, tx *sqlx.Tx, request dependencywrite.DependencyMutationRequest,
) (dependencywrite.DependencyMutationResult, error) {
	ids := []string{request.DependentTaskID, request.PredecessorTaskID}
	sort.Strings(ids)
	revisions, err := r.dependencyRevisionsInTx(ctx, tx, request.WorkspaceID, ids)
	if err != nil {
		return dependencywrite.DependencyMutationResult{}, err
	}
	var exists int
	err = tx.QueryRowContext(ctx, r.db.Rebind(`SELECT 1 FROM task_blockers WHERE task_id = ? AND blocker_task_id = ?`),
		request.DependentTaskID, request.PredecessorTaskID).Scan(&exists)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return dependencywrite.DependencyMutationResult{}, err
	}
	changed := (request.Operation == dependencyAdd && exists == 0) || (request.Operation == dependencyRemove && exists == 1)
	if changed {
		if err := r.writeDependencyChangeInTx(ctx, tx, request); err != nil {
			return dependencywrite.DependencyMutationResult{}, err
		}
		if err := r.incrementDependencyRevisionsInTx(ctx, tx, ids, revisions); err != nil {
			return dependencywrite.DependencyMutationResult{}, err
		}
	}
	result := dependencywrite.DependencyMutationResult{
		Changed: changed, DependentRevision: revisions[request.DependentTaskID],
		PredecessorRevision: revisions[request.PredecessorTaskID],
	}
	result.Dependent, err = r.dependencySnapshotInTx(ctx, tx, request.DependentTaskID, result.DependentRevision)
	if err != nil {
		return dependencywrite.DependencyMutationResult{}, err
	}
	result.Predecessor, err = r.dependencySnapshotInTx(ctx, tx, request.PredecessorTaskID, result.PredecessorRevision)
	if err != nil {
		return dependencywrite.DependencyMutationResult{}, err
	}
	if err := r.recordDependencyMutationSuccess(ctx, tx, request, result); err != nil {
		return dependencywrite.DependencyMutationResult{}, err
	}
	return result, nil
}

func (r *Repository) dependencyRevisionsInTx(ctx context.Context, tx *sqlx.Tx,
	workspaceID string, ids []string) (map[string]int64, error) {
	revisions := make(map[string]int64, len(ids))
	for _, id := range ids {
		var taskWorkspaceID string
		var revision int64
		query := `SELECT workspace_id, dependency_revision FROM tasks WHERE id = ?`
		if dialect.IsPostgres(r.db.DriverName()) {
			query += forUpdateClause
		}
		if err := tx.QueryRowContext(ctx, r.db.Rebind(query), id).Scan(&taskWorkspaceID, &revision); err != nil {
			return nil, err
		}
		if taskWorkspaceID != workspaceID {
			return nil, sql.ErrNoRows
		}
		revisions[id] = revision
	}
	return revisions, nil
}

func (r *Repository) incrementDependencyRevisionsInTx(ctx context.Context, tx *sqlx.Tx,
	ids []string, revisions map[string]int64) error {
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, r.db.Rebind(`UPDATE tasks SET dependency_revision = dependency_revision + 1 WHERE id = ?`), id); err != nil {
			return err
		}
		revisions[id]++
	}
	return nil
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
