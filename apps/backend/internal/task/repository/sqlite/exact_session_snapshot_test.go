package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
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

func TestExactSessionMessageSnapshotBindsIdentityAndMaterializesSanitizedRows(t *testing.T) {
	repo := newRepoForArchiveTests(t, "exact-session-messages")
	ctx := context.Background()
	createExactSnapshotSession(t, repo, "exact-session-messages", "session-messages")
	seedForMsgTest(t, repo, "exact-session-messages", "session-messages", "turn-messages")
	if err := repo.CreateMessage(ctx, &models.Message{
		ID: "message-visible", TaskSessionID: "session-messages", TaskID: "exact-session-messages",
		TurnID: "turn-messages", AuthorType: models.MessageAuthorAgent, Content: "visible <kandev-system>secret-token</kandev-system>",
	}); err != nil {
		t.Fatal(err)
	}

	reader, ok := any(repo).(interface {
		OpenExactSessionMessageSnapshot(context.Context, models.ExactSessionMessageSnapshotRequest) (*models.ExactSessionMessageSnapshot, error)
		PageExactSessionMessageSnapshot(context.Context, models.ExactSessionMessageSnapshotPageRequest) ([]models.ExactSessionMessageSnapshotMessage, error)
	})
	if !ok {
		t.Fatal("Repository does not expose the private exact session message snapshot reader")
	}
	session, err := repo.GetTaskSession(ctx, "session-messages")
	if err != nil {
		t.Fatal(err)
	}
	identity := models.ExactSessionMessageSnapshotRequest{
		InstallationID: "installation-a", WorkspaceID: archiveWorkspaceID, TaskID: "exact-session-messages", SessionID: "session-messages",
		QueueIncarnationID: session.QueueIncarnationID, RouteGeneration: session.RouteGeneration, SessionResourceVersion: exactSessionResourceVersion(t, repo, "session-messages"),
	}
	snapshot, err := reader.OpenExactSessionMessageSnapshot(ctx, identity)
	if err != nil {
		t.Fatalf("OpenExactSessionMessageSnapshot: %v", err)
	}
	rows, err := reader.PageExactSessionMessageSnapshot(ctx, models.ExactSessionMessageSnapshotPageRequest{ExactSessionMessageSnapshotRequest: identity, Token: snapshot.Token, Limit: 10})
	if err != nil || len(rows) != 1 || rows[0].Content != "visible" {
		t.Fatalf("sanitized rows = %#v, %v", rows, err)
	}
	foreign := identity
	foreign.InstallationID = "installation-b"
	if _, err := reader.PageExactSessionMessageSnapshot(ctx, models.ExactSessionMessageSnapshotPageRequest{ExactSessionMessageSnapshotRequest: foreign, Token: snapshot.Token, Limit: 10}); !errors.Is(err, repoerrors.ErrExactSessionMessageSnapshotUnavailable) {
		t.Fatalf("foreign installation error = %v, want unavailable", err)
	}
}

func TestExactSessionMessageSnapshotRejectsStaleGenerationAndIgnoresLaterMessages(t *testing.T) {
	repo := newRepoForArchiveTests(t, "exact-session-message-mutation")
	ctx := context.Background()
	createExactSnapshotSession(t, repo, "exact-session-message-mutation", "session-message-mutation")
	seedForMsgTest(t, repo, "exact-session-message-mutation", "session-message-mutation", "turn-message-mutation")
	insertPluginMsg(t, repo, "message-before", "session-message-mutation", "exact-session-message-mutation", "turn-message-mutation", "agent", "message", "before", time.Now().UTC())
	identity := exactSessionMessageSnapshotIdentity(t, repo, "installation-a", "exact-session-message-mutation", "session-message-mutation")
	snapshot, err := repo.OpenExactSessionMessageSnapshot(ctx, identity)
	if err != nil {
		t.Fatal(err)
	}
	insertPluginMsg(t, repo, "message-after", "session-message-mutation", "exact-session-message-mutation", "turn-message-mutation", "agent", "message", "after", time.Now().UTC().Add(time.Second))
	rows, err := repo.PageExactSessionMessageSnapshot(ctx, models.ExactSessionMessageSnapshotPageRequest{ExactSessionMessageSnapshotRequest: identity, Token: snapshot.Token, Limit: 10})
	if err != nil || len(rows) != 1 || rows[0].ID != "message-before" {
		t.Fatalf("materialized rows after append = %#v, %v", rows, err)
	}
	if err := repo.UpdateTaskSessionState(ctx, "session-message-mutation", models.TaskSessionStateRunning, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.OpenExactSessionMessageSnapshot(ctx, identity); !errors.Is(err, repoerrors.ErrExactSessionMessageSnapshotUnavailable) {
		t.Fatalf("open with stale generation error = %v, want unavailable", err)
	}
	if _, err := repo.PageExactSessionMessageSnapshot(ctx, models.ExactSessionMessageSnapshotPageRequest{ExactSessionMessageSnapshotRequest: identity, Token: snapshot.Token, Limit: 10}); !errors.Is(err, repoerrors.ErrExactSessionMessageSnapshotUnavailable) {
		t.Fatalf("stale generation error = %v, want unavailable", err)
	}
}

func TestExactSessionMessageSnapshotExpiryAndRestart(t *testing.T) {
	repo, sqlxDB, dbPath := newInitialTaskBriefRepoAtPath(t)
	ctx := context.Background()
	const workspaceID = "exact-session-message-restart-workspace"
	seedWorkspace(t, repo, workspaceID)
	if err := repo.CreateTask(ctx, &models.Task{ID: "exact-session-message-restart-task", WorkspaceID: workspaceID, Title: "restart"}); err != nil {
		t.Fatal(err)
	}
	createExactSnapshotSession(t, repo, "exact-session-message-restart-task", "exact-session-message-restart")
	seedForMsgTest(t, repo, "exact-session-message-restart-task", "exact-session-message-restart", "turn-message-restart")
	insertPluginMsg(t, repo, "message-restart", "exact-session-message-restart", "exact-session-message-restart-task", "turn-message-restart", "agent", "message", "durable", time.Now().UTC())
	base := time.Date(2026, time.September, 28, 0, 0, 0, 0, time.UTC)
	repo.clockNow = func() time.Time { return base }
	identity := exactSessionMessageSnapshotIdentity(t, repo, "installation-a", "exact-session-message-restart-task", "exact-session-message-restart")
	identity.WorkspaceID = workspaceID
	expired, err := repo.OpenExactSessionMessageSnapshot(ctx, models.ExactSessionMessageSnapshotRequest{InstallationID: identity.InstallationID, WorkspaceID: identity.WorkspaceID, TaskID: identity.TaskID, SessionID: identity.SessionID, QueueIncarnationID: identity.QueueIncarnationID, RouteGeneration: identity.RouteGeneration, SessionResourceVersion: identity.SessionResourceVersion, TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	repo.clockNow = func() time.Time { return base.Add(time.Minute) }
	if _, err := repo.PageExactSessionMessageSnapshot(ctx, models.ExactSessionMessageSnapshotPageRequest{ExactSessionMessageSnapshotRequest: identity, Token: expired.Token, Limit: 10}); !errors.Is(err, repoerrors.ErrExactSessionMessageSnapshotUnavailable) {
		t.Fatalf("expired snapshot error = %v, want unavailable", err)
	}
	repo.clockNow = nil
	fresh, err := repo.OpenExactSessionMessageSnapshot(ctx, identity)
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
	rows, err := reopened.PageExactSessionMessageSnapshot(ctx, models.ExactSessionMessageSnapshotPageRequest{ExactSessionMessageSnapshotRequest: identity, Token: fresh.Token, Limit: 10})
	if err != nil || len(rows) != 1 || rows[0].Content != "durable" {
		t.Fatalf("restarted snapshot rows = %#v, %v", rows, err)
	}
}

func TestExactSessionMessageSnapshotFailsClosedOnPostgres(t *testing.T) {
	db, err := sql.Open("pgx", "postgres://not-used")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := &Repository{db: sqlx.NewDb(db, "pgx")}
	identity := models.ExactSessionMessageSnapshotRequest{InstallationID: "installation", WorkspaceID: "workspace", TaskID: "task", SessionID: "session", QueueIncarnationID: "incarnation", SessionResourceVersion: 1}
	if _, err := repo.OpenExactSessionMessageSnapshot(context.Background(), identity); !errors.Is(err, repoerrors.ErrExactSessionMessageSnapshotUnavailable) {
		t.Fatalf("OpenExactSessionMessageSnapshot postgres error = %v, want unavailable", err)
	}
	if _, err := repo.PageExactSessionMessageSnapshot(context.Background(), models.ExactSessionMessageSnapshotPageRequest{ExactSessionMessageSnapshotRequest: identity, Token: "token", Limit: 1}); !errors.Is(err, repoerrors.ErrExactSessionMessageSnapshotUnavailable) {
		t.Fatalf("PageExactSessionMessageSnapshot postgres error = %v, want unavailable", err)
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

func exactSessionMessageSnapshotIdentity(t *testing.T, repo *Repository, installationID, taskID, sessionID string) models.ExactSessionMessageSnapshotRequest {
	t.Helper()
	session, err := repo.GetTaskSession(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	return models.ExactSessionMessageSnapshotRequest{
		InstallationID: installationID, WorkspaceID: archiveWorkspaceID, TaskID: taskID, SessionID: sessionID,
		QueueIncarnationID: session.QueueIncarnationID, RouteGeneration: session.RouteGeneration,
		SessionResourceVersion: exactSessionResourceVersion(t, repo, sessionID),
	}
}
