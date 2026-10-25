package sqlite

import (
	"context"
	"testing"

	v1 "github.com/kandev/kandev/pkg/api/v1"
)

func TestTaskResourceVersionChangesOnlyAfterCommittedMutation(t *testing.T) {
	repo := newRepoForArchiveTests(t, "task-resource-version")
	ctx := context.Background()
	version := taskResourceVersion(t, repo, "task-resource-version")
	if version != 1 {
		t.Fatalf("initial version = %d, want 1", version)
	}
	if err := repo.UpdateTaskState(ctx, "task-resource-version", v1.TaskStateInProgress); err != nil {
		t.Fatal(err)
	}
	if got := taskResourceVersion(t, repo, "task-resource-version"); got != version+1 {
		t.Fatalf("updated version = %d, want %d", got, version+1)
	}
	if changed, err := repo.ArchiveTaskIfActive(ctx, "task-resource-version", "cascade"); err != nil || !changed {
		t.Fatalf("first archive = %v, %v", changed, err)
	}
	version = taskResourceVersion(t, repo, "task-resource-version")
	if changed, err := repo.ArchiveTaskIfActive(ctx, "task-resource-version", "cascade"); err != nil || changed {
		t.Fatalf("rejected archive = %v, %v", changed, err)
	}
	if got := taskResourceVersion(t, repo, "task-resource-version"); got != version {
		t.Fatalf("rejected CAS changed version to %d", got)
	}
}

func TestGetTaskReturnsResourceVersion(t *testing.T) {
	repo := newRepoForArchiveTests(t, "task-resource-version-get")
	task, err := repo.GetTask(context.Background(), "task-resource-version-get")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.ResourceVersion != 1 {
		t.Fatalf("ResourceVersion = %d, want 1", task.ResourceVersion)
	}
}

func taskResourceVersion(t *testing.T, repo *Repository, id string) int64 {
	t.Helper()
	var version int64
	if err := repo.db.QueryRowContext(context.Background(), repo.db.Rebind(`SELECT resource_version FROM tasks WHERE id = ?`), id).Scan(&version); err != nil {
		t.Fatal(err)
	}
	return version
}
