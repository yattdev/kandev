package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"

	"github.com/kandev/kandev/internal/office/models"
	taskmodels "github.com/kandev/kandev/internal/task/models"
	tasksqlite "github.com/kandev/kandev/internal/task/repository/sqlite"
)

func TestExactRelationSnapshotInvalidatesForTaskAndEdgeMutation(t *testing.T) {
	repo, taskRepo := newExactRelationRepos(t, ":memory:")
	ctx := context.Background()
	seedExactRelationTask(t, taskRepo, "ws-a", "a1")
	seedExactRelationTask(t, taskRepo, "ws-a", "a2")
	if err := repo.CreateTaskBlocker(ctx, &models.TaskBlocker{TaskID: "a1", BlockerTaskID: "a2"}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := repo.OpenExactRelationSnapshot(ctx, models.ExactRelationSnapshotRequest{WorkspaceID: "ws-a"})
	if err != nil {
		t.Fatal(err)
	}
	page, err := repo.PageExactRelationSnapshot(ctx, snapshot.Token, 0, 10)
	if err != nil || len(page) != 1 || page[0].TaskResourceVersion < 1 || page[0].ResourceVersion != 1 {
		t.Fatalf("page = %#v, %v", page, err)
	}
	if err := repo.DeleteTaskBlocker(ctx, "a1", "a2"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.PageExactRelationSnapshot(ctx, snapshot.Token, 0, 10); !errors.Is(err, ErrExactRelationSnapshotUnavailable) {
		t.Fatalf("after edge mutation = %v", err)
	}
	snapshot, err = repo.OpenExactRelationSnapshot(ctx, models.ExactRelationSnapshotRequest{WorkspaceID: "ws-a"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := taskRepo.GetTask(ctx, "a1")
	if err != nil {
		t.Fatal(err)
	}
	task.Description = "changed"
	if err := taskRepo.UpdateTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.PageExactRelationSnapshot(ctx, snapshot.Token, 0, 10); !errors.Is(err, ErrExactRelationSnapshotUnavailable) {
		t.Fatalf("after task mutation = %v", err)
	}
}

func TestExactRelationSnapshotRejectsCrossWorkspaceAndExpires(t *testing.T) {
	repo, taskRepo := newExactRelationRepos(t, ":memory:")
	ctx := context.Background()
	seedExactRelationTask(t, taskRepo, "ws-a", "a1")
	seedExactRelationTask(t, taskRepo, "ws-b", "b1")
	if err := repo.CreateTaskBlocker(ctx, &models.TaskBlocker{TaskID: "a1", BlockerTaskID: "b1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.OpenExactRelationSnapshot(ctx, models.ExactRelationSnapshotRequest{WorkspaceID: "ws-a"}); !errors.Is(err, ErrExactRelationCrossWorkspace) {
		t.Fatalf("cross workspace = %v", err)
	}
	if err := repo.DeleteTaskBlocker(ctx, "a1", "b1"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := repo.OpenExactRelationSnapshot(ctx, models.ExactRelationSnapshotRequest{WorkspaceID: "ws-a", TTL: time.Nanosecond})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	if _, err := repo.PageExactRelationSnapshot(ctx, snapshot.Token, 0, 1); !errors.Is(err, ErrExactRelationSnapshotUnavailable) {
		t.Fatalf("expired page = %v", err)
	}
	if count, err := repo.CleanupExpiredExactRelationSnapshots(ctx, 10); err != nil || count != 1 {
		t.Fatalf("cleanup = %d, %v", count, err)
	}
}

func TestExactRelationSnapshotSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "relations.db")
	repo, taskRepo := newExactRelationRepos(t, path)
	ctx := context.Background()
	seedExactRelationTask(t, taskRepo, "ws-a", "a1")
	seedExactRelationTask(t, taskRepo, "ws-a", "a2")
	if err := repo.CreateTaskBlocker(ctx, &models.TaskBlocker{TaskID: "a1", BlockerTaskID: "a2"}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := repo.OpenExactRelationSnapshot(ctx, models.ExactRelationSnapshotRequest{WorkspaceID: "ws-a"})
	if err != nil {
		t.Fatal(err)
	}
	repo, _ = newExactRelationRepos(t, path)
	items, err := repo.PageExactRelationSnapshot(ctx, snapshot.Token, 0, 1)
	if err != nil || len(items) != 1 {
		t.Fatalf("restart page = %#v, %v", items, err)
	}
}

func TestExactRelationSnapshotReadSerializesConcurrentMutation(t *testing.T) {
	repo, taskRepo := newExactRelationRepos(t, ":memory:")
	ctx := context.Background()
	seedExactRelationTask(t, taskRepo, "ws-a", "a1")
	seedExactRelationTask(t, taskRepo, "ws-a", "a2")
	if err := repo.CreateTaskBlocker(ctx, &models.TaskBlocker{TaskID: "a1", BlockerTaskID: "a2"}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := repo.OpenExactRelationSnapshot(ctx, models.ExactRelationSnapshotRequest{WorkspaceID: "ws-a"})
	if err != nil {
		t.Fatal(err)
	}
	fenceReached, release := make(chan struct{}), make(chan struct{})
	repo.exactRelationSnapshotReadAfterFenceHook = func() { close(fenceReached); <-release }
	read := make(chan error, 1)
	go func() { _, err := repo.PageExactRelationSnapshot(ctx, snapshot.Token, 0, 1); read <- err }()
	<-fenceReached
	mutation := make(chan error, 1)
	go func() { mutation <- repo.DeleteTaskBlocker(ctx, "a1", "a2") }()
	close(release)
	if err := <-read; err != nil {
		t.Fatalf("read = %v", err)
	}
	if err := <-mutation; err != nil {
		t.Fatalf("mutation = %v", err)
	}
	if _, err := repo.PageExactRelationSnapshot(ctx, snapshot.Token, 0, 1); !errors.Is(err, ErrExactRelationSnapshotUnavailable) {
		t.Fatalf("after mutation = %v", err)
	}
}

func newExactRelationRepos(t *testing.T, source string) (*Repository, *tasksqlite.Repository) {
	t.Helper()
	db, err := sqlx.Open("sqlite3", source)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	taskRepo, err := tasksqlite.NewWithDB(db, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	officeRepo, err := NewWithDB(db, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	return officeRepo, taskRepo
}

func seedExactRelationTask(t *testing.T, repo *tasksqlite.Repository, workspaceID, taskID string) {
	t.Helper()
	ctx := context.Background()
	if _, err := repo.GetWorkspace(ctx, workspaceID); err != nil {
		if err := repo.CreateWorkspace(ctx, &taskmodels.Workspace{ID: workspaceID, Name: workspaceID}); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.CreateTask(ctx, &taskmodels.Task{ID: taskID, WorkspaceID: workspaceID, Title: taskID}); err != nil {
		t.Fatal(err)
	}
}
