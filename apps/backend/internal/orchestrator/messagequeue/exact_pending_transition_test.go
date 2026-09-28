package messagequeue_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/kandev/kandev/internal/exactsnapshotauthority"
	"github.com/kandev/kandev/internal/office/models"
	officesqlite "github.com/kandev/kandev/internal/office/repository/sqlite"
	mq "github.com/kandev/kandev/internal/orchestrator/messagequeue"
	taskmodels "github.com/kandev/kandev/internal/task/models"
	tasksqlite "github.com/kandev/kandev/internal/task/repository/sqlite"
	_ "github.com/mattn/go-sqlite3"
)

type exactQueue interface {
	mq.Repository
	mq.ExactPendingTransitionReader
	mq.ExactPendingTransitionTransactionReader
	mq.ExactPendingTransitionAuthorityReader
}

func TestExactPendingTransitionUnavailableWithoutTaskBoundary(t *testing.T) {
	db, err := sqlx.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	repo, err := mq.NewSQLiteRepository(db, db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.(mq.ExactPendingTransitionReader).OpenExactPendingTransitionSnapshot(context.Background(), mq.ExactPendingTransitionSnapshotRequest{WorkspaceID: "ws"}); !errors.Is(err, mq.ErrExactPendingTransitionUnavailable) {
		t.Fatalf("err=%v", err)
	}
}

func TestExactPendingTransitionSnapshotLifecycle(t *testing.T) {
	q, task := newExactPendingQueue(t)
	ctx := context.Background()
	seedExactPendingTask(t, task, "ws", "task", "session")
	if err := q.SetPendingMove(ctx, "session", &mq.PendingMove{TaskID: "task", WorkflowID: "wf", WorkflowStepID: "step"}); err != nil {
		t.Fatal(err)
	}
	snap, err := q.OpenExactPendingTransitionSnapshot(ctx, mq.ExactPendingTransitionSnapshotRequest{WorkspaceID: "ws"})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := q.PageExactPendingTransitionSnapshot(ctx, snap.Token, 0, 1)
	if err != nil || len(rows) != 1 || rows[0].SessionIncarnationID == "" || rows[0].ResourceVersion != 1 {
		t.Fatalf("rows=%#v err=%v", rows, err)
	}
	if _, err := q.TakePendingMove(ctx, "session"); err != nil {
		t.Fatal(err)
	}
	if _, err := q.PageExactPendingTransitionSnapshot(ctx, snap.Token, 0, 1); !errors.Is(err, mq.ErrExactPendingTransitionUnavailable) {
		t.Fatalf("post mutation=%v", err)
	}
	snap, err = q.OpenExactPendingTransitionSnapshot(ctx, mq.ExactPendingTransitionSnapshotRequest{WorkspaceID: "ws", TTL: time.Nanosecond})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	if _, err := q.PageExactPendingTransitionSnapshot(ctx, snap.Token, 0, 1); !errors.Is(err, mq.ErrExactPendingTransitionUnavailable) {
		t.Fatalf("expired=%v", err)
	}
	if n, err := q.CleanupExpiredExactPendingTransitionSnapshots(ctx, 10); err != nil || n != 1 {
		t.Fatalf("cleanup=%d,%v", n, err)
	}
}

func TestExactSnapshotsRequireOneBootstrapAuthority(t *testing.T) {
	ctx := context.Background()
	db, err := sqlx.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	task, err := tasksqlite.NewWithDB(db, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	office, err := officesqlite.NewWithDB(db, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	queueRepo, err := mq.NewSQLiteRepository(db, db)
	if err != nil {
		t.Fatal(err)
	}
	queue := queueRepo.(exactQueue)
	seedExactPendingTask(t, task, "ws", "task", "session")
	if err := queue.SetPendingMove(ctx, "session", &mq.PendingMove{TaskID: "task"}); err != nil {
		t.Fatal(err)
	}
	authority, err := exactsnapshotauthority.NewSQLite(db)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := office.BeginExactRelationSnapshotAuthorityTx(ctx, authority)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	relations, err := office.OpenExactRelationSnapshotInAuthorityTx(ctx, authority, tx, models.ExactRelationSnapshotRequest{WorkspaceID: "ws"})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := queue.OpenExactPendingTransitionSnapshotInAuthorityTx(ctx, authority, tx, mq.ExactPendingTransitionSnapshotRequest{WorkspaceID: "ws"})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if rows, err := office.PageExactRelationSnapshot(ctx, relations.Token, 0, 1); err != nil || len(rows) != 0 {
		t.Fatalf("relations=%#v err=%v", rows, err)
	}
	if rows, err := queue.PageExactPendingTransitionSnapshot(ctx, pending.Token, 0, 1); err != nil || len(rows) != 1 {
		t.Fatalf("pending=%#v err=%v", rows, err)
	}

	otherDB, err := sqlx.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = otherDB.Close() })
	foreign, err := exactsnapshotauthority.NewSQLite(otherDB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := office.BeginExactRelationSnapshotAuthorityTx(ctx, foreign); !errors.Is(err, officesqlite.ErrExactRelationSnapshotUnavailable) {
		t.Fatalf("office foreign authority=%v", err)
	}
	if _, err := queue.BeginExactPendingTransitionSnapshotAuthorityTx(ctx, foreign); !errors.Is(err, mq.ErrExactPendingTransitionUnavailable) {
		t.Fatalf("queue foreign authority=%v", err)
	}
}

func TestExactSnapshotsAuthorityRollbackLeavesNoSnapshots(t *testing.T) {
	ctx := context.Background()
	db, err := sqlx.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	task, err := tasksqlite.NewWithDB(db, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	office, err := officesqlite.NewWithDB(db, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	queueRepo, err := mq.NewSQLiteRepository(db, db)
	if err != nil {
		t.Fatal(err)
	}
	queue := queueRepo.(exactQueue)
	seedExactPendingTask(t, task, "ws", "task", "session")
	if err := queue.SetPendingMove(ctx, "session", &mq.PendingMove{TaskID: "task"}); err != nil {
		t.Fatal(err)
	}
	authority, err := exactsnapshotauthority.NewSQLite(db)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := queue.BeginExactPendingTransitionSnapshotAuthorityTx(ctx, authority)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := queue.OpenExactPendingTransitionSnapshotInAuthorityTx(ctx, authority, tx, mq.ExactPendingTransitionSnapshotRequest{WorkspaceID: "ws"})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := queue.PageExactPendingTransitionSnapshot(ctx, pending.Token, 0, 1); !errors.Is(err, mq.ErrExactPendingTransitionUnavailable) {
		t.Fatalf("rolled back=%v", err)
	}
	if _, err := office.OpenExactRelationSnapshotInAuthorityTx(ctx, authority, nil, models.ExactRelationSnapshotRequest{WorkspaceID: "ws"}); !errors.Is(err, officesqlite.ErrExactRelationSnapshotUnavailable) {
		t.Fatalf("nil authority transaction=%v", err)
	}
}

func TestExactPendingTransitionSnapshotInTxRollbackLeavesNoSnapshot(t *testing.T) {
	q, task := newExactPendingQueue(t)
	ctx := context.Background()
	seedExactPendingTask(t, task, "ws", "task", "session")
	if err := q.SetPendingMove(ctx, "session", &mq.PendingMove{TaskID: "task"}); err != nil {
		t.Fatal(err)
	}
	tx, err := q.BeginExactPendingTransitionSnapshotTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := q.OpenExactPendingTransitionSnapshotInTx(ctx, tx, mq.ExactPendingTransitionSnapshotRequest{WorkspaceID: "ws"})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := q.PageExactPendingTransitionSnapshot(ctx, snapshot.Token, 0, 1); !errors.Is(err, mq.ErrExactPendingTransitionUnavailable) {
		t.Fatalf("rolled back=%v", err)
	}
}

func TestExactPendingTransitionSnapshotSurvivesQueueRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.db")
	q, task := newExactPendingQueueAt(t, path)
	ctx := context.Background()
	seedExactPendingTask(t, task, "ws", "task", "session")
	if err := q.SetPendingMove(ctx, "session", &mq.PendingMove{TaskID: "task"}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := q.OpenExactPendingTransitionSnapshot(ctx, mq.ExactPendingTransitionSnapshotRequest{WorkspaceID: "ws"})
	if err != nil {
		t.Fatal(err)
	}
	q, _ = newExactPendingQueueAt(t, path)
	if rows, err := q.PageExactPendingTransitionSnapshot(ctx, snapshot.Token, 0, 1); err != nil || len(rows) != 1 {
		t.Fatalf("restart rows=%#v err=%v", rows, err)
	}
}

func TestExactPendingTransitionConcurrentReadAndMutation(t *testing.T) {
	q, task := newExactPendingQueue(t)
	ctx := context.Background()
	seedExactPendingTask(t, task, "ws", "task", "session")
	if err := q.SetPendingMove(ctx, "session", &mq.PendingMove{TaskID: "task"}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := q.OpenExactPendingTransitionSnapshot(ctx, mq.ExactPendingTransitionSnapshotRequest{WorkspaceID: "ws"})
	if err != nil {
		t.Fatal(err)
	}
	read := make(chan error, 1)
	mutation := make(chan error, 1)
	go func() { _, err := q.PageExactPendingTransitionSnapshot(ctx, snapshot.Token, 0, 1); read <- err }()
	go func() { _, err := q.TakePendingMove(ctx, "session"); mutation <- err }()
	if err := <-read; err != nil && !errors.Is(err, mq.ErrExactPendingTransitionUnavailable) {
		t.Fatalf("read=%v", err)
	}
	if err := <-mutation; err != nil {
		t.Fatalf("mutation=%v", err)
	}
	if _, err := q.PageExactPendingTransitionSnapshot(ctx, snapshot.Token, 0, 1); !errors.Is(err, mq.ErrExactPendingTransitionUnavailable) {
		t.Fatalf("post mutation=%v", err)
	}
}

func newExactPendingQueue(t *testing.T) (exactQueue, *tasksqlite.Repository) {
	t.Helper()
	db, err := sqlx.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	task, err := tasksqlite.NewWithDB(db, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := mq.NewSQLiteRepository(db, db)
	if err != nil {
		t.Fatal(err)
	}
	return repo.(exactQueue), task
}

func newExactPendingQueueAt(t *testing.T, source string) (exactQueue, *tasksqlite.Repository) {
	t.Helper()
	db, err := sqlx.Open("sqlite3", source)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	task, err := tasksqlite.NewWithDB(db, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := mq.NewSQLiteRepository(db, db)
	if err != nil {
		t.Fatal(err)
	}
	return repo.(exactQueue), task
}
func seedExactPendingTask(t *testing.T, repo *tasksqlite.Repository, ws, taskID, sessionID string) {
	t.Helper()
	ctx := context.Background()
	if err := repo.CreateWorkspace(ctx, &taskmodels.Workspace{ID: ws, Name: ws}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateTask(ctx, &taskmodels.Task{ID: taskID, WorkspaceID: ws, Title: taskID}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateTaskSession(ctx, &taskmodels.TaskSession{ID: sessionID, TaskID: taskID, State: taskmodels.TaskSessionStateCreated}); err != nil {
		t.Fatal(err)
	}
}
