package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/kandev/kandev/internal/db/dialect"
	"github.com/kandev/kandev/internal/task/taskinstructions"
)

// GetTaskInstructionBinding returns one task's authoritative instruction
// binding. Callers must still verify the mirrored file and executor guard.
func (r *Repository) GetTaskInstructionBinding(ctx context.Context, taskID string) (*taskinstructions.DurableBinding, error) {
	binding, found, err := readTaskInstructionBinding(ctx, r.ro, r.ro.Rebind, taskID, false, r.ro.DriverName())
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, taskinstructions.ErrBindingNotFound
	}
	return binding, nil
}

// PutTaskInstructionBindingExact writes a new immutable version only when the
// task, workspace, environment ownership generation, and current instruction
// version still match the caller's observed fence.
func (r *Repository) PutTaskInstructionBindingExact(ctx context.Context, request taskinstructions.WriteRequest) (*taskinstructions.DurableBinding, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := r.validateInstructionBindingScope(ctx, tx, request); err != nil {
		return nil, err
	}
	current, found, err := readTaskInstructionBinding(ctx, tx, r.db.Rebind, request.TaskID, true, r.db.DriverName())
	if err != nil {
		return nil, err
	}
	if (!found && request.ExpectedVersion != 0) || (found && current.Version != request.ExpectedVersion) {
		return nil, taskinstructions.ErrVersionConflict
	}

	now := r.nowUTC()
	if !found {
		binding := newTaskInstructionBinding(request, now)
		if err := r.insertTaskInstructionBinding(ctx, tx, binding); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return binding, nil
	}

	if err := r.updateTaskInstructionBinding(ctx, tx, current, request, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return current, nil
}

func newTaskInstructionBinding(request taskinstructions.WriteRequest, now time.Time) *taskinstructions.DurableBinding {
	return &taskinstructions.DurableBinding{
		TaskID: request.TaskID, WorkspaceID: request.WorkspaceID, EnvironmentID: request.EnvironmentID,
		OwnershipGeneration: request.ExpectedOwnershipGeneration, Version: 1,
		Content: request.Content, Digest: request.Digest, ControlPath: request.ControlPath,
		GuardKind: request.GuardKind, CreatedAt: now, UpdatedAt: now,
	}
}

func (r *Repository) insertTaskInstructionBinding(ctx context.Context, tx *sqlx.Tx, binding *taskinstructions.DurableBinding) error {
	_, err := tx.ExecContext(ctx, r.db.Rebind(`
		INSERT INTO task_instruction_bindings (
			task_id, workspace_id, task_environment_id, ownership_generation, version,
			content, content_sha256, control_path, guard_kind, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`), binding.TaskID, binding.WorkspaceID, binding.EnvironmentID, binding.OwnershipGeneration,
		binding.Version, binding.Content, binding.Digest, binding.ControlPath, string(binding.GuardKind), binding.CreatedAt, binding.UpdatedAt)
	return err
}

func (r *Repository) updateTaskInstructionBinding(
	ctx context.Context, tx *sqlx.Tx, current *taskinstructions.DurableBinding,
	request taskinstructions.WriteRequest, now time.Time,
) error {
	current.Version++
	current.Content, current.Digest, current.ControlPath, current.GuardKind = request.Content, request.Digest, request.ControlPath, request.GuardKind
	current.UpdatedAt = now
	result, err := tx.ExecContext(ctx, r.db.Rebind(`
		UPDATE task_instruction_bindings
		SET version = ?, content = ?, content_sha256 = ?, control_path = ?, guard_kind = ?, updated_at = ?
		WHERE task_id = ? AND version = ?
	`), current.Version, current.Content, current.Digest, current.ControlPath, string(current.GuardKind), current.UpdatedAt, current.TaskID, request.ExpectedVersion)
	if err != nil {
		return err
	}
	if rows, err := result.RowsAffected(); err != nil || rows != 1 {
		if err != nil {
			return err
		}
		return taskinstructions.ErrVersionConflict
	}
	return nil
}

func (r *Repository) validateInstructionBindingScope(ctx context.Context, tx *sqlx.Tx, request taskinstructions.WriteRequest) error {
	taskQuery := `SELECT workspace_id FROM tasks WHERE id = ?`
	if dialect.IsPostgres(r.db.DriverName()) {
		taskQuery += forUpdateClause
	}
	var workspaceID string
	if err := tx.QueryRowContext(ctx, r.db.Rebind(taskQuery), request.TaskID).Scan(&workspaceID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return taskinstructions.ErrBindingNotFound
		}
		return err
	}
	if workspaceID != request.WorkspaceID {
		return taskinstructions.ErrBindingNotFound
	}
	var taskID string
	var generation int64
	environmentQuery := `SELECT task_id, ownership_generation FROM task_environments WHERE id = ?`
	if dialect.IsPostgres(r.db.DriverName()) {
		environmentQuery += forUpdateClause
	}
	if err := tx.QueryRowContext(ctx, r.db.Rebind(environmentQuery), request.EnvironmentID).Scan(&taskID, &generation); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return taskinstructions.ErrEnvironmentChanged
		}
		return err
	}
	if taskID != request.TaskID || generation != request.ExpectedOwnershipGeneration {
		return taskinstructions.ErrEnvironmentChanged
	}
	return nil
}

type taskInstructionQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readTaskInstructionBinding(
	ctx context.Context, queryer taskInstructionQueryer, rebind func(string) string,
	taskID string, lock bool, driver string,
) (*taskinstructions.DurableBinding, bool, error) {
	query := `SELECT task_id, workspace_id, task_environment_id, ownership_generation, version,
		content, content_sha256, control_path, guard_kind, created_at, updated_at
		FROM task_instruction_bindings WHERE task_id = ?`
	if lock && dialect.IsPostgres(driver) {
		query += forUpdateClause
	}
	binding := &taskinstructions.DurableBinding{}
	var guardKind string
	err := queryer.QueryRowContext(ctx, rebind(query), taskID).Scan(
		&binding.TaskID, &binding.WorkspaceID, &binding.EnvironmentID, &binding.OwnershipGeneration, &binding.Version,
		&binding.Content, &binding.Digest, &binding.ControlPath, &guardKind, &binding.CreatedAt, &binding.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read task instruction binding: %w", err)
	}
	binding.GuardKind = taskinstructions.GuardKind(guardKind)
	return binding, true, nil
}
