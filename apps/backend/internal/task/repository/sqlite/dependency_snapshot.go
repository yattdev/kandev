package sqlite

import (
	"context"
	"database/sql"

	"github.com/jmoiron/sqlx"

	"github.com/kandev/kandev/internal/task/dependencywrite"
	v1 "github.com/kandev/kandev/pkg/api/v1"
)

const (
	dependencySnapshotDisplayLimit = 512
	dependencySnapshotPending      = "pending"
	dependencySnapshotFailed       = "failed"
)

// dependencySnapshotInTx reads both directions after the edge write and before
// commit, so a later mutation cannot change this event's graph projection.
func (r *Repository) dependencySnapshotInTx(ctx context.Context, tx *sqlx.Tx,
	taskID string, revision int64) (dependencywrite.DependencySnapshot, error) {
	snapshot := dependencywrite.DependencySnapshot{TaskID: taskID, Revision: revision,
		DependsOn: []dependencywrite.DependencyRef{}, Blocks: []dependencywrite.DependencyRef{}}
	predecessors, err := tx.QueryContext(ctx, r.db.Rebind(`SELECT t.id, t.title, t.state, t.archived_at, t.workspace_id, t.workflow_id
		FROM task_blockers b JOIN tasks t ON t.id = b.blocker_task_id
		WHERE b.task_id = ? ORDER BY b.created_at, b.blocker_task_id`), taskID)
	if err != nil {
		return dependencywrite.DependencySnapshot{}, err
	}
	for predecessors.Next() {
		ref, archived, scanErr := scanDependencySnapshotRef(predecessors)
		if scanErr != nil {
			_ = predecessors.Close()
			return dependencywrite.DependencySnapshot{}, scanErr
		}
		ref.Status = dependencySnapshotStatus(ref.State, archived)
		switch ref.Status {
		case dependencySnapshotFailed:
			if snapshot.BlockedReason == "" {
				snapshot.BlockedReason = dependencySnapshotFailed
			}
		case dependencySnapshotPending:
			snapshot.BlockedReason = dependencySnapshotPending
		}
		if len(snapshot.DependsOn) < dependencySnapshotDisplayLimit {
			snapshot.DependsOn = append(snapshot.DependsOn, ref)
		} else {
			snapshot.DependsOnTruncated = true
		}
	}
	if err := predecessors.Err(); err != nil {
		_ = predecessors.Close()
		return dependencywrite.DependencySnapshot{}, err
	}
	if err := predecessors.Close(); err != nil {
		return dependencywrite.DependencySnapshot{}, err
	}
	snapshot.Blocked = snapshot.BlockedReason != ""
	dependents, err := tx.QueryContext(ctx, r.db.Rebind(`SELECT t.id, t.title, t.state, t.archived_at, t.workspace_id, t.workflow_id
		FROM task_blockers b JOIN tasks t ON t.id = b.task_id
		WHERE b.blocker_task_id = ? ORDER BY b.created_at, b.task_id LIMIT ?`),
		taskID, dependencySnapshotDisplayLimit+1)
	if err != nil {
		return dependencywrite.DependencySnapshot{}, err
	}
	defer func() { _ = dependents.Close() }()
	for dependents.Next() {
		ref, _, scanErr := scanDependencySnapshotRef(dependents)
		if scanErr != nil {
			return dependencywrite.DependencySnapshot{}, scanErr
		}
		if len(snapshot.Blocks) < dependencySnapshotDisplayLimit {
			snapshot.Blocks = append(snapshot.Blocks, ref)
		} else {
			snapshot.BlocksTruncated = true
		}
	}
	return snapshot, dependents.Err()
}

func scanDependencySnapshotRef(rows *sql.Rows) (dependencywrite.DependencyRef, bool, error) {
	var ref dependencywrite.DependencyRef
	var archived sql.NullTime
	err := rows.Scan(&ref.ID, &ref.Title, &ref.State, &archived, &ref.WorkspaceID, &ref.WorkflowID)
	return ref, archived.Valid, err
}

func dependencySnapshotStatus(state string, archived bool) string {
	switch state {
	case string(v1.TaskStateCompleted):
		if !archived {
			return "resolved"
		}
	case string(v1.TaskStateFailed), string(v1.TaskStateCancelled):
		return dependencySnapshotFailed
	}
	return dependencySnapshotPending
}
