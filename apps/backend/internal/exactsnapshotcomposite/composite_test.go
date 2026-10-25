package exactsnapshotcomposite_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/kandev/kandev/internal/exactsnapshotauthority"
	composite "github.com/kandev/kandev/internal/exactsnapshotcomposite"
	officemodels "github.com/kandev/kandev/internal/office/models"
	officesqlite "github.com/kandev/kandev/internal/office/repository/sqlite"
	mq "github.com/kandev/kandev/internal/orchestrator/messagequeue"
	taskmodels "github.com/kandev/kandev/internal/task/models"
	tasksqlite "github.com/kandev/kandev/internal/task/repository/sqlite"
	_ "github.com/mattn/go-sqlite3"
)

type exactQueue interface {
	mq.Repository
	mq.ExactPendingTransitionReader
	mq.ExactPendingTransitionAuthorityReader
}

func TestOpenCommitsOneCompositeSnapshot(t *testing.T) {
	ctx := context.Background()
	db := exactDB(t)
	tasks, err := tasksqlite.NewWithDB(db, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	office, err := officesqlite.NewWithDB(db, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	queueRepository, err := mq.NewSQLiteRepository(db, db)
	if err != nil {
		t.Fatal(err)
	}
	queue := queueRepository.(exactQueue)
	seed(t, tasks)
	if err := tasks.CreateTask(ctx, &taskmodels.Task{ID: "blocker", WorkspaceID: "ws", Title: "blocker"}); err != nil {
		t.Fatal(err)
	}
	if err := office.CreateTaskBlocker(ctx, &officemodels.TaskBlocker{TaskID: "task", BlockerTaskID: "blocker"}); err != nil {
		t.Fatal(err)
	}
	if err := queue.SetPendingMove(ctx, "session", &mq.PendingMove{TaskID: "task"}); err != nil {
		t.Fatal(err)
	}
	authority, err := exactsnapshotauthority.NewSQLite(db)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := composite.New(authority, office, queue)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := repo.Open(ctx, composite.Request{WorkspaceID: "ws"})
	if err != nil {
		t.Fatal(err)
	}
	page, err := repo.Page(ctx, snapshot.Token, 0, 1, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Relations) != 1 || len(page.PendingTransitions) != 1 {
		t.Fatalf("page=%#v", page)
	}
	if relation, err := repo.GetRelation(ctx, snapshot.Token, "task", "blocker"); err != nil || relation == nil {
		t.Fatalf("relation=%#v err=%v", relation, err)
	}
	if pending, err := repo.GetPendingTransition(ctx, snapshot.Token, "session"); err != nil || pending == nil {
		t.Fatalf("pending=%#v err=%v", pending, err)
	}
}

func exactDB(t *testing.T) *sqlx.DB {
	t.Helper()
	db, err := sqlx.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func seed(t *testing.T, repo *tasksqlite.Repository) {
	t.Helper()
	ctx := context.Background()
	if err := repo.CreateWorkspace(ctx, &taskmodels.Workspace{ID: "ws", Name: "ws"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateTask(ctx, &taskmodels.Task{ID: "task", WorkspaceID: "ws", Title: "task"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateTaskSession(ctx, &taskmodels.TaskSession{ID: "session", TaskID: "task", State: taskmodels.TaskSessionStateCreated}); err != nil {
		t.Fatal(err)
	}
}

func TestCompositeSnapshotFailsClosedAfterPendingMutation(t *testing.T) {
	ctx := context.Background()
	repo, queue, _, _ := newComposite(t, ":memory:", false, true)
	snapshot, err := repo.Open(ctx, composite.Request{WorkspaceID: "ws"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queue.TakePendingMove(ctx, "session"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Page(ctx, snapshot.Token, 0, 1, 0, 1); !errors.Is(err, composite.ErrUnavailable) {
		t.Fatalf("page after pending mutation=%v", err)
	}
}

func TestCompositeSnapshotFailsClosedAfterTaskSessionAndEdgeMutation(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name   string
		mutate func(t *testing.T, tasks *tasksqlite.Repository, office *officesqlite.Repository)
	}{
		{
			name: "task",
			mutate: func(t *testing.T, tasks *tasksqlite.Repository, _ *officesqlite.Repository) {
				task, err := tasks.GetTask(ctx, "task")
				if err != nil {
					t.Fatal(err)
				}
				task.Title = "changed"
				if err := tasks.UpdateTask(ctx, task); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "session",
			mutate: func(t *testing.T, tasks *tasksqlite.Repository, _ *officesqlite.Repository) {
				if err := tasks.UpdateTaskSessionState(ctx, "session", taskmodels.TaskSessionStateWaitingForInput, ""); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "edge",
			mutate: func(t *testing.T, tasks *tasksqlite.Repository, office *officesqlite.Repository) {
				if err := tasks.CreateTask(ctx, &taskmodels.Task{ID: "blocker", WorkspaceID: "ws", Title: "blocker"}); err != nil {
					t.Fatal(err)
				}
				if err := office.CreateTaskBlocker(ctx, &officemodels.TaskBlocker{TaskID: "task", BlockerTaskID: "blocker"}); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, _, tasks, office := newComposite(t, ":memory:", false, true)
			snapshot, err := repo.Open(ctx, composite.Request{WorkspaceID: "ws"})
			if err != nil {
				t.Fatal(err)
			}
			tt.mutate(t, tasks, office)
			if _, err := repo.Page(ctx, snapshot.Token, 0, 1, 0, 1); !errors.Is(err, composite.ErrUnavailable) {
				t.Fatalf("page after %s mutation=%v", tt.name, err)
			}
		})
	}
}

func TestCompositeSnapshotExpiresAndCleansUp(t *testing.T) {
	ctx := context.Background()
	repo, _, _, _ := newComposite(t, ":memory:", false, true)
	snapshot, err := repo.Open(ctx, composite.Request{WorkspaceID: "ws", TTL: time.Nanosecond})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	if _, err := repo.Page(ctx, snapshot.Token, 0, 1, 0, 1); !errors.Is(err, composite.ErrUnavailable) {
		t.Fatalf("expired page=%v", err)
	}
	if n, err := repo.CleanupExpired(ctx, 10); err != nil || n != 1 {
		t.Fatalf("cleanup=%d,%v", n, err)
	}
}

func TestCompositeSnapshotSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "exact-composite.db")
	repo, _, _, _ := newComposite(t, path, false, true)
	snapshot, err := repo.Open(ctx, composite.Request{WorkspaceID: "ws"})
	if err != nil {
		t.Fatal(err)
	}
	repo, _, _, _ = newComposite(t, path, false, false)
	page, err := repo.Page(ctx, snapshot.Token, 0, 1, 0, 1)
	if err != nil || len(page.PendingTransitions) != 1 {
		t.Fatalf("restart page=%#v err=%v", page, err)
	}
}

func TestCompositeSnapshotRollbackLeavesNoSourceOrCompositeToken(t *testing.T) {
	ctx := context.Background()
	db := exactDB(t)
	tasks, err := tasksqlite.NewWithDB(db, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	office, err := officesqlite.NewWithDB(db, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	queueRepository, err := mq.NewSQLiteRepository(db, db)
	if err != nil {
		t.Fatal(err)
	}
	queue := queueRepository.(exactQueue)
	seed(t, tasks)
	if err := queue.SetPendingMove(ctx, "session", &mq.PendingMove{TaskID: "task"}); err != nil {
		t.Fatal(err)
	}
	authority, err := exactsnapshotauthority.NewSQLite(db)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := composite.New(authority, office, queue)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := authority.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := repo.OpenInAuthorityTx(ctx, tx, composite.Request{WorkspaceID: "ws"})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Page(ctx, snapshot.Token, 0, 1, 0, 1); !errors.Is(err, composite.ErrUnavailable) {
		t.Fatalf("rolled back page=%v", err)
	}
}

func TestCompositeRejectsQueueStartedBeforeTaskSchema(t *testing.T) {
	db := exactDB(t)
	queueRepository, err := mq.NewSQLiteRepository(db, db)
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := tasksqlite.NewWithDB(db, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	office, err := officesqlite.NewWithDB(db, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	seed(t, tasks)
	authority, err := exactsnapshotauthority.NewSQLite(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := composite.New(authority, office, queueRepository.(exactQueue)); !errors.Is(err, composite.ErrUnavailable) {
		t.Fatalf("startup-order construction=%v", err)
	}
}

func TestCompositeSnapshotRejectsForeignAuthority(t *testing.T) {
	ctx := context.Background()
	db := exactDB(t)
	tasks, err := tasksqlite.NewWithDB(db, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	office, err := officesqlite.NewWithDB(db, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	queueRepository, err := mq.NewSQLiteRepository(db, db)
	if err != nil {
		t.Fatal(err)
	}
	seed(t, tasks)
	queue := queueRepository.(exactQueue)
	if err := queue.SetPendingMove(ctx, "session", &mq.PendingMove{TaskID: "task"}); err != nil {
		t.Fatal(err)
	}
	foreign, err := exactsnapshotauthority.NewSQLite(exactDB(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := composite.New(foreign, office, queue); !errors.Is(err, composite.ErrUnavailable) {
		t.Fatalf("foreign authority=%v", err)
	}
}

func TestCompositeSnapshotConcurrentReadAndMutation(t *testing.T) {
	ctx := context.Background()
	repo, queue, _, _ := newComposite(t, ":memory:", false, true)
	snapshot, err := repo.Open(ctx, composite.Request{WorkspaceID: "ws"})
	if err != nil {
		t.Fatal(err)
	}
	read := make(chan error, 1)
	mutate := make(chan error, 1)
	go func() { _, err := repo.Page(ctx, snapshot.Token, 0, 1, 0, 1); read <- err }()
	go func() { _, err := queue.TakePendingMove(ctx, "session"); mutate <- err }()
	if err := <-read; err != nil && !errors.Is(err, composite.ErrUnavailable) {
		t.Fatalf("read=%v", err)
	}
	if err := <-mutate; err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Page(ctx, snapshot.Token, 0, 1, 0, 1); !errors.Is(err, composite.ErrUnavailable) {
		t.Fatalf("post mutation=%v", err)
	}
}

func newComposite(t *testing.T, source string, queueFirst, seedData bool) (*composite.Repository, exactQueue, *tasksqlite.Repository, *officesqlite.Repository) {
	t.Helper()
	db, err := sqlx.Open("sqlite3", source)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	var queueRepository mq.Repository
	if queueFirst {
		queueRepository, err = mq.NewSQLiteRepository(db, db)
		if err != nil {
			t.Fatal(err)
		}
	}
	tasks, err := tasksqlite.NewWithDB(db, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	office, err := officesqlite.NewWithDB(db, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !queueFirst {
		queueRepository, err = mq.NewSQLiteRepository(db, db)
		if err != nil {
			t.Fatal(err)
		}
	}
	queue := queueRepository.(exactQueue)
	if seedData {
		seed(t, tasks)
		if !queueFirst {
			if err := queue.SetPendingMove(context.Background(), "session", &mq.PendingMove{TaskID: "task"}); err != nil {
				t.Fatal(err)
			}
		}
	}
	authority, err := exactsnapshotauthority.NewSQLite(db)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := composite.New(authority, office, queue)
	if err != nil {
		t.Fatal(err)
	}
	return repo, queue, tasks, office
}
