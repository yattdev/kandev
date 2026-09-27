package sqlite

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/kandev/kandev/internal/db"
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

func TestExactTaskSnapshotRejectsReadsAtExpiry(t *testing.T) {
	repo := newRepoForArchiveTests(t, "exact-snapshot-expiry")
	base := time.Date(2026, time.September, 27, 0, 0, 0, 0, time.UTC)
	repo.clockNow = func() time.Time { return base }
	snapshot, err := repo.OpenExactTaskSnapshot(context.Background(), models.ExactTaskSnapshotRequest{WorkspaceID: archiveWorkspaceID, TTL: time.Minute})
	if err != nil {
		t.Fatalf("OpenExactTaskSnapshot: %v", err)
	}
	repo.clockNow = func() time.Time { return base.Add(time.Minute) }
	if _, err := repo.PageExactTaskSnapshot(context.Background(), snapshot.Token, 0, 1); !errors.Is(err, repoerrors.ErrExactTaskSnapshotUnavailable) {
		t.Fatalf("PageExactTaskSnapshot at expiry error = %v, want ErrExactTaskSnapshotUnavailable", err)
	}
}

func TestExactTaskSnapshotSurvivesRepositoryRestart(t *testing.T) {
	repo, sqlxDB, dbPath := newInitialTaskBriefRepoAtPath(t)
	ctx := context.Background()
	const workspaceID = "exact-snapshot-restart-workspace"
	seedWorkspace(t, repo, workspaceID)
	if err := repo.CreateTask(ctx, &models.Task{ID: "exact-snapshot-restart-task", WorkspaceID: workspaceID, Title: "restart"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	snapshot, err := repo.OpenExactTaskSnapshot(ctx, models.ExactTaskSnapshotRequest{WorkspaceID: workspaceID})
	if err != nil {
		t.Fatalf("OpenExactTaskSnapshot: %v", err)
	}
	if err := sqlxDB.Close(); err != nil {
		t.Fatalf("close first repository: %v", err)
	}
	conn, err := db.OpenSQLite(dbPath)
	if err != nil {
		t.Fatalf("reopen sqlite: %v", err)
	}
	reopenedDB := sqlx.NewDb(conn, "sqlite3")
	t.Cleanup(func() { _ = reopenedDB.Close() })
	reopened, err := NewWithDB(reopenedDB, reopenedDB, nil)
	if err != nil {
		t.Fatalf("reopen repository: %v", err)
	}
	if _, err := reopened.PageExactTaskSnapshot(ctx, snapshot.Token, 0, 1); err != nil {
		t.Fatalf("PageExactTaskSnapshot after restart: %v", err)
	}
	if _, err := reopened.GetExactTaskSnapshotTask(ctx, snapshot.Token, "exact-snapshot-restart-task"); err != nil {
		t.Fatalf("GetExactTaskSnapshotTask after restart: %v", err)
	}
}

func TestExactTaskSnapshotReadSerializesConcurrentWorkspaceMutation(t *testing.T) {
	repo := newRepoForArchiveTests(t, "exact-snapshot-race")
	ctx := context.Background()
	snapshot, err := repo.OpenExactTaskSnapshot(ctx, models.ExactTaskSnapshotRequest{WorkspaceID: archiveWorkspaceID})
	if err != nil {
		t.Fatalf("OpenExactTaskSnapshot: %v", err)
	}
	fenceReached := make(chan struct{})
	releaseRead := make(chan struct{})
	repo.exactSnapshotReadAfterFenceHook = func() { close(fenceReached); <-releaseRead }
	type pageResult struct {
		items []models.ExactTaskSnapshotTask
		err   error
	}
	readResult := make(chan pageResult, 1)
	go func() {
		items, err := repo.PageExactTaskSnapshot(ctx, snapshot.Token, 0, 1)
		readResult <- pageResult{items: items, err: err}
	}()
	<-fenceReached
	mutationResult := make(chan error, 1)
	go func() { mutationResult <- repo.UpdateTaskState(ctx, "exact-snapshot-race", v1.TaskStateInProgress) }()
	close(releaseRead)
	read := <-readResult
	if read.err != nil || len(read.items) != 1 || read.items[0].State != "" {
		t.Fatalf("PageExactTaskSnapshot = %#v, %v", read.items, read.err)
	}
	if err := <-mutationResult; err != nil {
		t.Fatalf("UpdateTaskState: %v", err)
	}
	if _, err := repo.PageExactTaskSnapshot(ctx, snapshot.Token, 0, 1); !errors.Is(err, repoerrors.ErrExactTaskSnapshotUnavailable) {
		t.Fatalf("PageExactTaskSnapshot after mutation error = %v, want ErrExactTaskSnapshotUnavailable", err)
	}
}
