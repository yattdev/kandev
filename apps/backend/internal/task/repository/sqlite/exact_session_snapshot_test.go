package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/kandev/kandev/internal/db"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository/repoerrors"
)

func TestExactSessionSnapshotIsWorkspaceBoundAndComplete(t *testing.T) {
	repo := newRepoForArchiveTests(t, "exact-session-first", "exact-session-second")
	ctx := context.Background()
	createExactSnapshotSession(t, repo, "exact-session-first", "session-first")
	createExactSnapshotSession(t, repo, "exact-session-second", "session-second")
	otherWorkspace := "exact-session-other-workspace"
	seedWorkspace(t, repo, otherWorkspace)
	if err := repo.CreateTask(ctx, &models.Task{ID: "exact-session-other-task", WorkspaceID: otherWorkspace, Title: "other"}); err != nil {
		t.Fatal(err)
	}
	createExactSnapshotSession(t, repo, "exact-session-other-task", "session-other")

	snapshot, err := repo.OpenExactSessionSnapshot(ctx, models.ExactSessionSnapshotRequest{WorkspaceID: archiveWorkspaceID})
	if err != nil {
		t.Fatalf("OpenExactSessionSnapshot: %v", err)
	}
	first, err := repo.PageExactSessionSnapshot(ctx, snapshot.Token, 0, 1)
	if err != nil || len(first) != 1 {
		t.Fatalf("first page = %+v, %v", first, err)
	}
	second, err := repo.PageExactSessionSnapshot(ctx, snapshot.Token, 1, 2)
	if err != nil || len(second) != 1 {
		t.Fatalf("second page = %+v, %v", second, err)
	}
	for _, session := range append(first, second...) {
		if session.WorkspaceID != archiveWorkspaceID || session.ResourceVersion != 1 {
			t.Fatalf("session = %+v, want workspace-bound initial version", session)
		}
	}
	foreign, err := repo.GetExactSessionSnapshotSession(ctx, snapshot.Token, "session-other")
	if err != nil || foreign != nil {
		t.Fatalf("foreign snapshot session = %+v, %v", foreign, err)
	}
}

func TestExactSessionSnapshotRejectsMutationAndTracksResourceVersion(t *testing.T) {
	repo := newRepoForArchiveTests(t, "exact-session-mutation")
	ctx := context.Background()
	createExactSnapshotSession(t, repo, "exact-session-mutation", "session-mutation")
	before := exactSessionResourceVersion(t, repo, "session-mutation")
	if before != 1 {
		t.Fatalf("initial version = %d, want 1", before)
	}
	snapshot, err := repo.OpenExactSessionSnapshot(ctx, models.ExactSessionSnapshotRequest{WorkspaceID: archiveWorkspaceID})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateTaskSessionState(ctx, "session-mutation", models.TaskSessionStateRunning, ""); err != nil {
		t.Fatal(err)
	}
	if got := exactSessionResourceVersion(t, repo, "session-mutation"); got != before+1 {
		t.Fatalf("updated version = %d, want %d", got, before+1)
	}
	if _, err := repo.PageExactSessionSnapshot(ctx, snapshot.Token, 0, 1); !errors.Is(err, repoerrors.ErrExactSessionSnapshotUnavailable) {
		t.Fatalf("page after mutation error = %v, want unavailable", err)
	}
}

func TestExactSessionSnapshotExpiryCleanupAndRestart(t *testing.T) {
	repo, sqlxDB, dbPath := newInitialTaskBriefRepoAtPath(t)
	ctx := context.Background()
	const workspaceID = "exact-session-restart-workspace"
	seedWorkspace(t, repo, workspaceID)
	if err := repo.CreateTask(ctx, &models.Task{ID: "exact-session-restart-task", WorkspaceID: workspaceID, Title: "restart"}); err != nil {
		t.Fatal(err)
	}
	createExactSnapshotSession(t, repo, "exact-session-restart-task", "exact-session-restart")
	base := time.Date(2026, time.September, 27, 0, 0, 0, 0, time.UTC)
	repo.clockNow = func() time.Time { return base }
	expired, err := repo.OpenExactSessionSnapshot(ctx, models.ExactSessionSnapshotRequest{WorkspaceID: workspaceID, TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	repo.clockNow = func() time.Time { return base.Add(time.Minute) }
	if _, err := repo.PageExactSessionSnapshot(ctx, expired.Token, 0, 1); !errors.Is(err, repoerrors.ErrExactSessionSnapshotUnavailable) {
		t.Fatalf("page at expiry error = %v, want unavailable", err)
	}
	_, err = repo.OpenExactSessionSnapshot(ctx, models.ExactSessionSnapshotRequest{WorkspaceID: workspaceID})
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := repo.db.QueryRowContext(ctx, repo.db.Rebind(`SELECT COUNT(*) FROM exact_session_snapshots WHERE token = ?`), expired.Token).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expired snapshots = %d, want 0", count)
	}
	repo.clockNow = nil
	fresh, err := repo.OpenExactSessionSnapshot(ctx, models.ExactSessionSnapshotRequest{WorkspaceID: workspaceID})
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlxDB.Close(); err != nil {
		t.Fatal(err)
	}
	conn, err := db.OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	reopenedDB := sqlx.NewDb(conn, "sqlite3")
	t.Cleanup(func() { _ = reopenedDB.Close() })
	reopened, err := NewWithDB(reopenedDB, reopenedDB, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.GetExactSessionSnapshotSession(ctx, fresh.Token, "exact-session-restart"); err != nil {
		t.Fatalf("GetExactSessionSnapshotSession after restart: %v", err)
	}
}

func TestExactSessionSnapshotReadSerializesConcurrentMutation(t *testing.T) {
	repo := newRepoForArchiveTests(t, "exact-session-race")
	ctx := context.Background()
	createExactSnapshotSession(t, repo, "exact-session-race", "session-race")
	snapshot, err := repo.OpenExactSessionSnapshot(ctx, models.ExactSessionSnapshotRequest{WorkspaceID: archiveWorkspaceID})
	if err != nil {
		t.Fatal(err)
	}
	fenceReached := make(chan struct{})
	releaseRead := make(chan struct{})
	repo.exactSessionSnapshotReadAfterFenceHook = func() { close(fenceReached); <-releaseRead }
	type result struct {
		items []models.ExactSessionSnapshotSession
		err   error
	}
	readResult := make(chan result, 1)
	go func() {
		items, err := repo.PageExactSessionSnapshot(ctx, snapshot.Token, 0, 1)
		readResult <- result{items: items, err: err}
	}()
	<-fenceReached
	mutation := make(chan error, 1)
	go func() {
		mutation <- repo.UpdateTaskSessionState(ctx, "session-race", models.TaskSessionStateRunning, "")
	}()
	close(releaseRead)
	read := <-readResult
	if read.err != nil || len(read.items) != 1 || read.items[0].State != models.TaskSessionStateCreated {
		t.Fatalf("PageExactSessionSnapshot = %#v, %v", read.items, read.err)
	}
	if err := <-mutation; err != nil {
		t.Fatal(err)
	}
	if _, err := repo.PageExactSessionSnapshot(ctx, snapshot.Token, 0, 1); !errors.Is(err, repoerrors.ErrExactSessionSnapshotUnavailable) {
		t.Fatalf("page after mutation error = %v, want unavailable", err)
	}
}

func createExactSnapshotSession(t *testing.T, repo *Repository, taskID, sessionID string) {
	t.Helper()
	if err := repo.CreateTaskSession(context.Background(), &models.TaskSession{ID: sessionID, TaskID: taskID, State: models.TaskSessionStateCreated}); err != nil {
		t.Fatalf("CreateTaskSession(%s): %v", sessionID, err)
	}
}

func exactSessionResourceVersion(t *testing.T, repo *Repository, sessionID string) int64 {
	t.Helper()
	var version int64
	if err := repo.db.QueryRowContext(context.Background(), repo.db.Rebind(`SELECT resource_version FROM task_sessions WHERE id = ?`), sessionID).Scan(&version); err != nil {
		t.Fatal(err)
	}
	return version
}
