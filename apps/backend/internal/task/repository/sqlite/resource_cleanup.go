package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/recoveryclaim"
	"github.com/kandev/kandev/internal/task/repository/repoerrors"
)

const taskResourceCleanupColumns = `
	id, operation_id, task_id, trigger, state, resource_snapshot, attempts,
	next_attempt_at, last_error, created_at, updated_at, completed_at`

func (r *Repository) CreateTaskResourceCleanupJob(ctx context.Context, job *models.TaskResourceCleanupJob) error {
	if job == nil {
		return errors.New("task resource cleanup job is nil")
	}
	if job.OperationID == "" || job.Trigger == "" {
		return errors.New("task cleanup reservation requires task, operation, and trigger")
	}
	workspaceOnly := job.TaskID == "" && job.Trigger == models.TaskResourceCleanupTriggerWorkspaceDelete
	if job.TaskID == "" && !workspaceOnly {
		return errors.New("task cleanup reservation requires a task")
	}
	workspaceID := ""
	if workspaceOnly {
		var snapshot struct {
			WorkspaceID string `json:"workspace_id"`
		}
		if err := json.Unmarshal([]byte(job.ResourceSnapshot), &snapshot); err != nil || snapshot.WorkspaceID == "" {
			return errors.New("workspace cleanup reservation requires a workspace snapshot")
		}
		workspaceID = snapshot.WorkspaceID
		if strings.HasPrefix(job.OperationID, "workspace_delete_admission:") &&
			job.OperationID != models.WorkspaceDeleteAdmissionOperationID(workspaceID) {
			return fmt.Errorf("%w: workspace fence identity", errCleanupReservationConflict)
		}
	}
	if job.ID == "" {
		job.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	job.CreatedAt = now
	job.UpdatedAt = now
	if job.State == "" {
		job.State = models.TaskResourceCleanupStatePending
	}
	if !validTaskResourceCleanupState(job.State) {
		return fmt.Errorf("%w: unsupported reservation state %s", errCleanupReservationConflict, job.State)
	}
	workspaceFence := workspaceOnly && job.OperationID == models.WorkspaceDeleteAdmissionOperationID(workspaceID)
	if workspaceFence && job.State != models.TaskResourceCleanupStatePrepared {
		return fmt.Errorf("%w: workspace delete fence must be prepared", errCleanupReservationConflict)
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if workspaceOnly {
		existing, err := r.admitWorkspaceCleanupReservation(ctx, tx, workspaceID, job)
		if err != nil {
			return err
		}
		if existing {
			return tx.Commit()
		}
	}
	found := workspaceOnly
	if !workspaceOnly {
		if err := recoveryclaim.EnsureTaskAvailableTx(ctx, r.db, tx, job.TaskID); err != nil {
			return err
		}
		held, taskFound, err := r.taskRetentionInTx(ctx, tx, job.TaskID)
		if err != nil {
			return err
		}
		if held {
			return fmt.Errorf("%w: %s", errTaskRetentionHeld, job.TaskID)
		}
		found = taskFound
	}
	if !found {
		var existingTaskID string
		var existingTrigger models.TaskResourceCleanupTrigger
		var existingState models.TaskResourceCleanupState
		err := tx.QueryRowContext(ctx, r.db.Rebind(`
			SELECT task_id, trigger, state FROM task_resource_cleanup_jobs WHERE operation_id = ?
		`), job.OperationID).Scan(&existingTaskID, &existingTrigger, &existingState)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: %s", repoerrors.ErrTaskNotFound, job.TaskID)
		}
		if err != nil {
			return err
		}
		if existingTaskID != job.TaskID || existingTrigger != job.Trigger || !validTaskResourceCleanupState(existingState) {
			return fmt.Errorf("%w: %s", errCleanupReservationConflict, job.OperationID)
		}
		return tx.Commit()
	}
	inserted, err := tx.ExecContext(ctx, r.db.Rebind(`
		INSERT INTO task_resource_cleanup_jobs (`+taskResourceCleanupColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(operation_id) DO NOTHING
	`), job.ID, job.OperationID, job.TaskID, job.Trigger, job.State,
		job.ResourceSnapshot, job.Attempts, job.NextAttemptAt, job.LastError,
		job.CreatedAt, job.UpdatedAt, job.CompletedAt)
	if err != nil {
		return err
	}
	if workspaceFence {
		if err := r.restoreCancelledWorkspaceFence(ctx, tx, inserted, job, now, workspaceID); err != nil {
			return err
		}
	}
	var existingTaskID string
	var existingTrigger models.TaskResourceCleanupTrigger
	var existingState models.TaskResourceCleanupState
	if err := tx.QueryRowContext(ctx, r.db.Rebind(`
		SELECT task_id, trigger, state FROM task_resource_cleanup_jobs WHERE operation_id = ?
	`), job.OperationID).Scan(&existingTaskID, &existingTrigger, &existingState); err != nil {
		return err
	}
	if existingTaskID != job.TaskID || existingTrigger != job.Trigger || !validTaskResourceCleanupState(existingState) {
		return fmt.Errorf("%w: %s", errCleanupReservationConflict, job.OperationID)
	}
	return tx.Commit()
}

func (r *Repository) restoreCancelledWorkspaceFence(
	ctx context.Context, tx *sqlx.Tx, inserted sql.Result, job *models.TaskResourceCleanupJob,
	now time.Time, workspaceID string,
) error {
	rows, err := inserted.RowsAffected()
	if err != nil || rows != 0 {
		return err
	}
	restored, err := tx.ExecContext(ctx, r.db.Rebind(`
		UPDATE task_resource_cleanup_jobs
		SET state = ?, resource_snapshot = ?, last_error = '', next_attempt_at = NULL,
			completed_at = NULL, updated_at = ?
		WHERE operation_id = ? AND task_id = '' AND trigger = ? AND state = ?
	`), models.TaskResourceCleanupStatePrepared, job.ResourceSnapshot, now,
		job.OperationID, job.Trigger, models.TaskResourceCleanupStateCancelled)
	if err != nil {
		return err
	}
	rows, err = restored.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return fmt.Errorf("%w: workspace %s", repoerrors.ErrTaskCleanupInProgress, workspaceID)
	}
	return nil
}

func (r *Repository) admitWorkspaceCleanupReservation(
	ctx context.Context, tx *sqlx.Tx, workspaceID string, job *models.TaskResourceCleanupJob,
) (bool, error) {
	if err := r.lockWorkspaceRowInTx(ctx, tx, workspaceID); err != nil &&
		!errors.Is(err, repoerrors.ErrWorkspaceNotFound) {
		return false, err
	}
	var foundWorkspaceID string
	err := tx.QueryRowContext(ctx, r.db.Rebind(`SELECT id FROM workspaces WHERE id = ?`), workspaceID).Scan(&foundWorkspaceID)
	if err == nil {
		return false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if job.OperationID == models.WorkspaceDeleteAdmissionOperationID(workspaceID) {
		return false, fmt.Errorf("%w: %s", repoerrors.ErrWorkspaceNotFound, workspaceID)
	}
	var existingTaskID string
	var existingTrigger models.TaskResourceCleanupTrigger
	var existingState models.TaskResourceCleanupState
	err = tx.QueryRowContext(ctx, r.db.Rebind(`
		SELECT task_id, trigger, state FROM task_resource_cleanup_jobs WHERE operation_id = ?
	`), job.OperationID).Scan(&existingTaskID, &existingTrigger, &existingState)
	if errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("%w: %s", repoerrors.ErrWorkspaceNotFound, workspaceID)
	}
	if err != nil {
		return false, err
	}
	if existingTaskID != "" || existingTrigger != job.Trigger || !validTaskResourceCleanupState(existingState) {
		return false, fmt.Errorf("%w: %s", errCleanupReservationConflict, job.OperationID)
	}
	return true, nil
}

func validTaskResourceCleanupState(state models.TaskResourceCleanupState) bool {
	switch state {
	case models.TaskResourceCleanupStatePrepared, models.TaskResourceCleanupStatePending,
		models.TaskResourceCleanupStateRunning, models.TaskResourceCleanupStateRetryWait,
		models.TaskResourceCleanupStateSucceeded, models.TaskResourceCleanupStateFailed,
		models.TaskResourceCleanupStateCancelled:
		return true
	default:
		return false
	}
}

// UpdateTaskResourceCleanupSnapshot writes the resource inventory captured
// after the prepared barrier was reserved. The barrier row exists before the
// inventory query so concurrent session/worktree creation is rejected while
// the snapshot is being assembled.
func (r *Repository) UpdateTaskResourceCleanupSnapshot(ctx context.Context, operationID, snapshot string) error {
	job, err := r.GetTaskResourceCleanupJobByOperationID(ctx, operationID)
	if err != nil {
		return err
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := r.lockCleanupTaskInTx(ctx, tx, job.ID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, r.db.Rebind(`
		UPDATE task_resource_cleanup_jobs
		SET resource_snapshot = ?, updated_at = ?
		WHERE id = ? AND operation_id = ? AND state = ?
	`), snapshot, time.Now().UTC(), job.ID, operationID, models.TaskResourceCleanupStatePrepared)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("task resource cleanup job %s not found or not prepared", operationID)
	}
	return tx.Commit()
}

// UpdateClaimedTaskResourceCleanupSnapshot persists outcomes produced by one
// exact running cleanup attempt. A newer retry or cancellation wins when the
// claim no longer matches.
func (r *Repository) UpdateClaimedTaskResourceCleanupSnapshot(ctx context.Context, id string, attempt int, snapshot string) (bool, error) {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := r.lockCleanupTaskInTx(ctx, tx, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	result, err := tx.ExecContext(ctx, r.db.Rebind(`
		UPDATE task_resource_cleanup_jobs
		SET resource_snapshot = ?, updated_at = ?
		WHERE id = ? AND state = ? AND attempts = ?
	`), snapshot, time.Now().UTC(), id, models.TaskResourceCleanupStateRunning, attempt)
	if err != nil {
		return false, err
	}
	rows, _ := result.RowsAffected()
	return rows == 1, tx.Commit()
}

// HasActiveTaskResourceCleanupJob reports whether teardown has been admitted
// for a task. The prepared state is included because the cleanup intent is
// persisted before task deletion and before the worker is allowed to run.
func (r *Repository) HasActiveTaskResourceCleanupJob(ctx context.Context, taskID string) (bool, error) {
	var active bool
	err := r.ro.QueryRowContext(ctx, r.ro.Rebind(`
		SELECT EXISTS (
			SELECT 1 FROM task_resource_cleanup_jobs
			WHERE task_id = ? AND state IN (?, ?, ?, ?)
		)
	`), taskID,
		models.TaskResourceCleanupStatePrepared,
		models.TaskResourceCleanupStatePending,
		models.TaskResourceCleanupStateRunning,
		models.TaskResourceCleanupStateRetryWait,
	).Scan(&active)
	return active, err
}

func (r *Repository) GetTaskResourceCleanupJobByOperationID(ctx context.Context, operationID string) (*models.TaskResourceCleanupJob, error) {
	row := r.ro.QueryRowContext(ctx, r.ro.Rebind(`
		SELECT `+taskResourceCleanupColumns+`
		FROM task_resource_cleanup_jobs WHERE operation_id = ?
	`), operationID)
	return scanTaskResourceCleanupJob(row)
}

func (r *Repository) GetTaskResourceCleanupJob(ctx context.Context, id string) (*models.TaskResourceCleanupJob, error) {
	row := r.ro.QueryRowContext(ctx, r.ro.Rebind(`
		SELECT `+taskResourceCleanupColumns+`
		FROM task_resource_cleanup_jobs WHERE id = ?
	`), id)
	return scanTaskResourceCleanupJob(row)
}

func (r *Repository) ListArchiveTaskResourceCleanupJobs(
	ctx context.Context, taskID string,
) ([]*models.TaskResourceCleanupJob, error) {
	rows, err := r.ro.QueryContext(ctx, r.ro.Rebind(`
		SELECT `+taskResourceCleanupColumns+`
		FROM task_resource_cleanup_jobs
		WHERE task_id = ? AND trigger IN (?, ?)
		ORDER BY created_at ASC
	`), taskID, models.TaskResourceCleanupTriggerArchive,
		models.TaskResourceCleanupTriggerCascadeArchive)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	jobs := make([]*models.TaskResourceCleanupJob, 0)
	for rows.Next() {
		job, scanErr := scanTaskResourceCleanupJob(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

// ListTaskResourceCleanupJobs returns every durable cleanup generation for one
// task, including delete generations that outlive the task row itself.
func (r *Repository) ListTaskResourceCleanupJobs(
	ctx context.Context, taskID string,
) ([]*models.TaskResourceCleanupJob, error) {
	rows, err := r.ro.QueryContext(ctx, r.ro.Rebind(`
		SELECT `+taskResourceCleanupColumns+`
		FROM task_resource_cleanup_jobs
		WHERE task_id = ?
		ORDER BY created_at ASC
	`), taskID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	jobs := make([]*models.TaskResourceCleanupJob, 0)
	for rows.Next() {
		job, scanErr := scanTaskResourceCleanupJob(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (r *Repository) ListPreparedTaskResourceCleanupJobs(ctx context.Context) ([]*models.TaskResourceCleanupJob, error) {
	rows, err := r.ro.QueryContext(ctx, r.ro.Rebind(`
		SELECT `+taskResourceCleanupColumns+`
		FROM task_resource_cleanup_jobs
		WHERE state = ? ORDER BY created_at ASC
	`), models.TaskResourceCleanupStatePrepared)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	jobs := make([]*models.TaskResourceCleanupJob, 0)
	for rows.Next() {
		job, scanErr := scanTaskResourceCleanupJob(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func scanTaskResourceCleanupJob(row interface{ Scan(...any) error }) (*models.TaskResourceCleanupJob, error) {
	job := &models.TaskResourceCleanupJob{}
	err := row.Scan(&job.ID, &job.OperationID, &job.TaskID, &job.Trigger, &job.State,
		&job.ResourceSnapshot, &job.Attempts, &job.NextAttemptAt, &job.LastError,
		&job.CreatedAt, &job.UpdatedAt, &job.CompletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("task resource cleanup job not found")
	}
	return job, err
}

func (r *Repository) ListDueTaskResourceCleanupJobs(ctx context.Context, now time.Time, limit int) ([]*models.TaskResourceCleanupJob, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.ro.QueryContext(ctx, r.ro.Rebind(`
		SELECT `+taskResourceCleanupColumns+`
		FROM task_resource_cleanup_jobs
		WHERE state = ? OR (state = ? AND (next_attempt_at IS NULL OR next_attempt_at <= ?))
		ORDER BY created_at ASC LIMIT ?
	`), models.TaskResourceCleanupStatePending, models.TaskResourceCleanupStateRetryWait, now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	jobs := make([]*models.TaskResourceCleanupJob, 0)
	for rows.Next() {
		job, scanErr := scanTaskResourceCleanupJob(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (r *Repository) MarkTaskResourceCleanupJobRunning(ctx context.Context, id string) (bool, error) {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	taskID, err := r.lockCleanupTaskInTx(ctx, tx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx, r.db.Rebind(`
		UPDATE task_resource_cleanup_jobs
		SET state = ?, attempts = attempts + 1, next_attempt_at = NULL, updated_at = ?
		WHERE id = ? AND task_id = ? AND state IN (?, ?)
	`), models.TaskResourceCleanupStateRunning, now, id, taskID,
		models.TaskResourceCleanupStatePending, models.TaskResourceCleanupStateRetryWait)
	if err != nil {
		return false, err
	}
	count, _ := result.RowsAffected()
	return count == 1, tx.Commit()
}

func (r *Repository) StartPreparedTaskResourceCleanupJob(ctx context.Context, id string) (bool, error) {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	taskID, err := r.lockCleanupTaskInTx(ctx, tx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx, r.db.Rebind(`
		UPDATE task_resource_cleanup_jobs
		SET state = ?, next_attempt_at = NULL, updated_at = ?
		WHERE id = ? AND task_id = ? AND state = ?
	`), models.TaskResourceCleanupStatePending, now, id, taskID, models.TaskResourceCleanupStatePrepared)
	if err != nil {
		return false, err
	}
	count, _ := result.RowsAffected()
	return count == 1, tx.Commit()
}

func (r *Repository) CompleteTaskResourceCleanupJob(
	ctx context.Context,
	id string,
	state models.TaskResourceCleanupState,
	lastError string,
	nextAttemptAt *time.Time,
) error {
	switch state {
	case models.TaskResourceCleanupStatePrepared, models.TaskResourceCleanupStateCancelled,
		models.TaskResourceCleanupStateSucceeded, models.TaskResourceCleanupStateFailed,
		models.TaskResourceCleanupStateRetryWait:
	default:
		return fmt.Errorf("%w: unsupported cleanup completion state %s", errCleanupReservationConflict, state)
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := r.lockCleanupTaskInTx(ctx, tx, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: cleanup %s is missing", errCleanupReservationConflict, id)
		}
		return err
	}
	now := time.Now().UTC()
	var completedAt *time.Time
	if state == models.TaskResourceCleanupStateSucceeded ||
		state == models.TaskResourceCleanupStateFailed ||
		state == models.TaskResourceCleanupStateCancelled {
		completedAt = &now
	}
	result, err := tx.ExecContext(ctx, r.db.Rebind(`
		UPDATE task_resource_cleanup_jobs
		SET state = ?, last_error = ?, next_attempt_at = ?, completed_at = ?, updated_at = ?
		WHERE id = ? AND state IN (?, ?)
	`), state, lastError, nextAttemptAt, completedAt, now, id,
		models.TaskResourceCleanupStatePrepared, models.TaskResourceCleanupStatePending)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return fmt.Errorf("%w: cleanup %s no longer has an unclaimed state", errCleanupReservationConflict, id)
	}
	return tx.Commit()
}

// RestoreCancelledTaskResourceCleanupJobIfUnchanged re-prepares only the
// cancelled cleanup generation that the caller inspected. The state and
// attempt predicates prevent a delayed restore from overwriting a newer
// prepared, pending, or running generation.
func (r *Repository) RestoreCancelledTaskResourceCleanupJobIfUnchanged(
	ctx context.Context,
	id string,
	attempts int,
	lastError string,
) (bool, error) {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	taskID, err := r.lockCleanupTaskInTx(ctx, tx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx, r.db.Rebind(`
		UPDATE task_resource_cleanup_jobs
		SET state = ?, last_error = ?, next_attempt_at = NULL, completed_at = NULL, updated_at = ?
		WHERE id = ? AND task_id = ? AND state = ? AND attempts = ?
	`), models.TaskResourceCleanupStatePrepared, lastError, now,
		id, taskID, models.TaskResourceCleanupStateCancelled, attempts)
	if err != nil {
		return false, err
	}
	count, _ := result.RowsAffected()
	return count == 1, tx.Commit()
}

// CancelTaskResourceCleanupJobIfPending cancels only an eligible cleanup
// generation. Running claims are left untouched for physical reconciliation.
func (r *Repository) CancelTaskResourceCleanupJobIfPending(ctx context.Context, id string) (bool, error) {
	now := time.Now().UTC()
	result, err := r.db.ExecContext(ctx, r.db.Rebind(`
		UPDATE task_resource_cleanup_jobs
		SET state = ?, next_attempt_at = NULL, completed_at = ?, updated_at = ?
		WHERE id = ? AND state IN (?, ?, ?)
	`),
		models.TaskResourceCleanupStateCancelled, now, now, id,
		models.TaskResourceCleanupStatePrepared,
		models.TaskResourceCleanupStatePending,
		models.TaskResourceCleanupStateRetryWait,
	)
	if err != nil {
		return false, err
	}
	count, _ := result.RowsAffected()
	return count == 1, nil
}

// CompleteClaimedTaskResourceCleanupJob applies a worker result only to the
// exact running claim that produced it. A concurrent cancellation or a newer
// retry generation wins and keeps its state and historical metadata.
func (r *Repository) CompleteClaimedTaskResourceCleanupJob(
	ctx context.Context,
	id string,
	attempt int,
	state models.TaskResourceCleanupState,
	lastError string,
	nextAttemptAt *time.Time,
) (bool, error) {
	switch state {
	case models.TaskResourceCleanupStateSucceeded, models.TaskResourceCleanupStateFailed,
		models.TaskResourceCleanupStateRetryWait, models.TaskResourceCleanupStateCancelled:
	default:
		return false, fmt.Errorf("%w: unsupported claimed completion state %s", errCleanupReservationConflict, state)
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := r.lockCleanupTaskInTx(ctx, tx, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	now := time.Now().UTC()
	var completedAt *time.Time
	if state == models.TaskResourceCleanupStateSucceeded ||
		state == models.TaskResourceCleanupStateFailed ||
		state == models.TaskResourceCleanupStateCancelled {
		completedAt = &now
	}
	result, err := tx.ExecContext(ctx, r.db.Rebind(`
		UPDATE task_resource_cleanup_jobs
		SET state = ?, last_error = ?, next_attempt_at = ?, completed_at = ?, updated_at = ?
		WHERE id = ? AND state = ? AND attempts = ?
	`), state, lastError, nextAttemptAt, completedAt, now, id,
		models.TaskResourceCleanupStateRunning, attempt)
	if err != nil {
		return false, err
	}
	count, _ := result.RowsAffected()
	return count == 1, tx.Commit()
}

func (r *Repository) CancelArchiveTaskResourceCleanupJobs(ctx context.Context, taskID string) error {
	now := time.Now().UTC()
	_, err := r.db.ExecContext(ctx, r.db.Rebind(`
		UPDATE task_resource_cleanup_jobs
		SET state = ?, completed_at = ?, updated_at = ?
		WHERE task_id = ? AND trigger IN (?, ?) AND state IN (?, ?, ?)
	`), models.TaskResourceCleanupStateCancelled, now, now, taskID,
		models.TaskResourceCleanupTriggerArchive, models.TaskResourceCleanupTriggerCascadeArchive,
		models.TaskResourceCleanupStatePrepared, models.TaskResourceCleanupStatePending,
		models.TaskResourceCleanupStateRetryWait)
	return err
}

func (r *Repository) ResetRunningTaskResourceCleanupJobs(ctx context.Context) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, r.db.Rebind(`
		SELECT id, task_id FROM task_resource_cleanup_jobs WHERE state = ?
	`), models.TaskResourceCleanupStateRunning)
	if err != nil {
		return err
	}
	type runningJob struct{ id, taskID string }
	jobs := make([]runningJob, 0)
	for rows.Next() {
		var job runningJob
		if err := rows.Scan(&job.id, &job.taskID); err != nil {
			_ = rows.Close()
			return err
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()
	sort.Slice(jobs, func(i, j int) bool {
		if jobs[i].taskID != jobs[j].taskID {
			return jobs[i].taskID < jobs[j].taskID
		}
		return jobs[i].id < jobs[j].id
	})
	now := time.Now().UTC()
	for _, job := range jobs {
		if _, err := r.lockCleanupTaskInTx(ctx, tx, job.id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, r.db.Rebind(`
			UPDATE task_resource_cleanup_jobs
			SET state = ?, next_attempt_at = ?, updated_at = ?
			WHERE id = ? AND task_id = ? AND state = ?
		`), models.TaskResourceCleanupStateRetryWait, now, now, job.id, job.taskID,
			models.TaskResourceCleanupStateRunning); err != nil {
			return err
		}
	}
	return tx.Commit()
}
