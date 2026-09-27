package sqlite

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository/repoerrors"
	v1 "github.com/kandev/kandev/pkg/api/v1"
)

// The first Exact reader contract is deliberately a repository capability,
// rather than another method on TaskRepository. A snapshot must reject later
// pages once its workspace changes; returning materialized stale rows is not
// an acceptable pagination result.
func TestExactTaskSnapshotRejectsPageAfterWorkspaceMutation(t *testing.T) {
	repo := newRepoForArchiveTests(t, "exact-snapshot-first", "exact-snapshot-second")
	if method := reflect.ValueOf(repo).MethodByName("OpenExactTaskSnapshot"); !method.IsValid() {
		t.Fatal("OpenExactTaskSnapshot is required before exact snapshot pagination can be safe")
	}
	ctx := context.Background()
	snapshot, err := repo.OpenExactTaskSnapshot(ctx, models.ExactTaskSnapshotRequest{WorkspaceID: archiveWorkspaceID})
	if err != nil {
		t.Fatalf("OpenExactTaskSnapshot: %v", err)
	}
	first, err := repo.PageExactTaskSnapshot(ctx, snapshot.Token, 0, 1)
	if err != nil || len(first) != 1 {
		t.Fatalf("first page = %+v, %v", first, err)
	}
	if err := repo.UpdateTaskState(ctx, "exact-snapshot-second", v1.TaskStateInProgress); err != nil {
		t.Fatalf("UpdateTaskState: %v", err)
	}
	if _, err := repo.PageExactTaskSnapshot(ctx, snapshot.Token, 1, 1); !errors.Is(err, repoerrors.ErrExactTaskSnapshotUnavailable) {
		t.Fatalf("page after workspace mutation error = %v, want ErrExactTaskSnapshotUnavailable", err)
	}
}

func TestExactTaskSnapshotRejectsGetAfterEveryWorkspaceMutation(t *testing.T) {
	repo := newRepoForArchiveTests(t, "exact-snapshot-update", "exact-snapshot-archive", "exact-snapshot-delete")
	ctx := context.Background()
	testCases := []struct {
		name   string
		mutate func(t *testing.T)
	}{
		{
			name: "insert",
			mutate: func(t *testing.T) {
				t.Helper()
				if err := repo.CreateTask(ctx, &models.Task{ID: "exact-snapshot-insert", WorkspaceID: archiveWorkspaceID, Title: "insert"}); err != nil {
					t.Fatalf("CreateTask: %v", err)
				}
			},
		},
		{
			name: "update",
			mutate: func(t *testing.T) {
				t.Helper()
				if err := repo.UpdateTaskState(ctx, "exact-snapshot-update", v1.TaskStateInProgress); err != nil {
					t.Fatalf("UpdateTaskState: %v", err)
				}
			},
		},
		{
			name: "archive",
			mutate: func(t *testing.T) {
				t.Helper()
				if err := repo.ArchiveTask(ctx, "exact-snapshot-archive"); err != nil {
					t.Fatalf("ArchiveTask: %v", err)
				}
			},
		},
		{
			name: "delete",
			mutate: func(t *testing.T) {
				t.Helper()
				if err := repo.DeleteTask(ctx, "exact-snapshot-delete"); err != nil {
					t.Fatalf("DeleteTask: %v", err)
				}
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			snapshot, err := repo.OpenExactTaskSnapshot(ctx, models.ExactTaskSnapshotRequest{WorkspaceID: archiveWorkspaceID, IncludeArchived: true})
			if err != nil {
				t.Fatalf("OpenExactTaskSnapshot: %v", err)
			}
			testCase.mutate(t)
			if _, err := repo.GetExactTaskSnapshotTask(ctx, snapshot.Token, "exact-snapshot-update"); !errors.Is(err, repoerrors.ErrExactTaskSnapshotUnavailable) {
				t.Fatalf("GetExactTaskSnapshotTask after %s error = %v, want ErrExactTaskSnapshotUnavailable", testCase.name, err)
			}
		})
	}
}
