package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	sqlite3 "github.com/mattn/go-sqlite3"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository/repoerrors"
)

var overlapDriverSequence atomic.Uint64

type overlapGate struct {
	entered     chan struct{}
	release     chan struct{}
	enteredOnce sync.Once
	releaseOnce sync.Once
}

func newOverlapGate() *overlapGate {
	return &overlapGate{entered: make(chan struct{}), release: make(chan struct{})}
}

func (g *overlapGate) wait() {
	g.enteredOnce.Do(func() { close(g.entered) })
	<-g.release
}

func (g *overlapGate) open() { g.releaseOnce.Do(func() { close(g.release) }) }

type overlapControl struct {
	sync.Mutex
	commit *overlapGate
	exec   *overlapGate
	match  string
}

type overlapDriver struct{ control *overlapControl }

func (d overlapDriver) Open(name string) (driver.Conn, error) {
	conn, err := (&sqlite3.SQLiteDriver{}).Open(name)
	if err != nil {
		return nil, err
	}
	return &overlapConn{SQLiteConn: conn.(*sqlite3.SQLiteConn), control: d.control}, nil
}

type overlapConn struct {
	*sqlite3.SQLiteConn
	control *overlapControl
}

func (c *overlapConn) Begin() (driver.Tx, error) {
	tx, err := c.SQLiteConn.Begin()
	if err != nil {
		return nil, err
	}
	return &overlapTx{Tx: tx, control: c.control}, nil
}

func (c *overlapConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	tx, err := c.SQLiteConn.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &overlapTx{Tx: tx, control: c.control}, nil
}

func (c *overlapConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	c.control.Lock()
	gate, match := c.control.exec, c.control.match
	c.control.Unlock()
	if gate != nil && strings.Contains(query, match) {
		gate.wait()
	}
	return c.SQLiteConn.ExecContext(ctx, query, args)
}

func (c *overlapConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.control.Lock()
	gate, match := c.control.exec, c.control.match
	c.control.Unlock()
	if gate != nil && strings.Contains(query, match) {
		gate.wait()
	}
	return c.SQLiteConn.QueryContext(ctx, query, args)
}

type overlapTx struct {
	driver.Tx
	control *overlapControl
}

func (tx *overlapTx) Commit() error {
	tx.control.Lock()
	gate := tx.control.commit
	tx.control.Unlock()
	if gate != nil {
		gate.wait()
	}
	return tx.Tx.Commit()
}

func newOverlappingSQLitePair(t *testing.T) (*Repository, *Repository, *overlapControl, *overlapControl) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "overlap.db")
	open := func() (*Repository, *overlapControl) {
		control := &overlapControl{}
		name := fmt.Sprintf("retention-overlap-%d", overlapDriverSequence.Add(1))
		sql.Register(name, overlapDriver{control: control})
		raw, err := sql.Open(name, "file:"+path+"?_foreign_keys=on&_busy_timeout=5000&_journal_mode=WAL")
		if err != nil {
			t.Fatal(err)
		}
		raw.SetMaxOpenConns(1)
		t.Cleanup(func() { _ = raw.Close() })
		db := sqlx.NewDb(raw, "sqlite3")
		repo, err := NewWithDB(db, db, nil)
		if err != nil {
			t.Fatal(err)
		}
		return repo, control
	}
	first, firstControl := open()
	second, secondControl := open()
	return first, second, firstControl, secondControl
}

func awaitOverlap(t *testing.T, entered <-chan struct{}) {
	t.Helper()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("transaction did not reach controlled boundary")
	}
}

func overlapResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("overlapping transaction did not finish")
		return nil
	}
}

func runOverlappingTransactions(t *testing.T, firstControl, secondControl *overlapControl,
	secondMatch string, first, second func() error,
) (error, error) {
	t.Helper()
	firstGate, secondGate := newOverlapGate(), newOverlapGate()
	firstControl.commit = firstGate
	secondControl.exec, secondControl.match = secondGate, secondMatch
	defer firstGate.open()
	defer secondGate.open()
	firstResult := make(chan error, 1)
	go func() { firstResult <- first() }()
	awaitOverlap(t, firstGate.entered)
	secondResult := make(chan error, 1)
	go func() { secondResult <- second() }()
	awaitOverlap(t, secondGate.entered)
	firstGate.open()
	firstErr := overlapResult(t, firstResult)
	secondGate.open()
	return firstErr, overlapResult(t, secondResult)
}

func TestRetentionAdmission_OverlappingSQLiteWinnerOrders(t *testing.T) {
	for _, holdFirst := range []bool{true, false} {
		t.Run(fmt.Sprintf("holdFirst=%t", holdFirst), func(t *testing.T) {
			first, second, firstControl, secondControl := newOverlappingSQLitePair(t)
			ctx := context.Background()
			seedBarrierTask(t, first, "overlap-parent")
			seedBarrierTask(t, first, "overlap-child")
			if _, err := first.db.ExecContext(ctx, `UPDATE tasks SET parent_id = ? WHERE id = ?`, "overlap-parent", "overlap-child"); err != nil {
				t.Fatal(err)
			}
			task, err := first.GetTask(ctx, "overlap-child")
			if err != nil {
				t.Fatal(err)
			}
			hold := func(repo *Repository) error {
				return repo.UpdateTaskWithParentRetention(ctx, task, "overlap-parent", "ws-1", true, false)
			}
			reserve := func(repo *Repository) error {
				return repo.CreateTaskResourceCleanupJob(ctx, &models.TaskResourceCleanupJob{
					OperationID: "overlap-cleanup", TaskID: task.ID,
					Trigger: models.TaskResourceCleanupTriggerArchive, State: models.TaskResourceCleanupStatePrepared,
				})
			}
			var firstErr, secondErr error
			if holdFirst {
				firstErr, secondErr = runOverlappingTransactions(t, firstControl, secondControl,
					"UPDATE tasks", func() error { return hold(first) }, func() error { return reserve(second) })
			} else {
				firstErr, secondErr = runOverlappingTransactions(t, firstControl, secondControl,
					"UPDATE tasks", func() error { return reserve(first) }, func() error { return hold(second) })
			}
			if firstErr != nil || secondErr == nil {
				t.Fatalf("first=%v second=%v, want committed winner and rejected loser", firstErr, secondErr)
			}
			assertRetentionOverlapOutcome(t, first, holdFirst, secondErr)
		})
	}
}

func assertRetentionOverlapOutcome(t *testing.T, repo *Repository, holdFirst bool, loserErr error) {
	t.Helper()
	ctx := context.Background()
	current, err := repo.GetTask(ctx, "overlap-child")
	if err != nil {
		t.Fatal(err)
	}
	held := models.IsTerminalRetentionHeld(current.Metadata)
	if holdFirst {
		var jobs int
		if err := repo.db.GetContext(ctx, &jobs,
			`SELECT COUNT(*) FROM task_resource_cleanup_jobs WHERE operation_id = ?`, "overlap-cleanup"); err != nil {
			t.Fatal(err)
		}
		if !errors.Is(loserErr, errTaskRetentionHeld) || !held || jobs != 0 {
			t.Fatalf("hold winner changed cleanup state: loser=%v held=%t jobs=%d", loserErr, held, jobs)
		}
		return
	}
	job, err := repo.GetTaskResourceCleanupJobByOperationID(ctx, "overlap-cleanup")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(loserErr.Error(), "task cleanup history prevents retention admission") ||
		held || job.State != models.TaskResourceCleanupStatePrepared {
		t.Fatalf("cleanup winner changed hold state: loser=%v held=%t job=%+v", loserErr, held, job)
	}
}

func TestWorkspaceDeleteCreationFence_OverlappingSQLiteWinnerOrders(t *testing.T) {
	for _, deleteFirst := range []bool{true, false} {
		t.Run(fmt.Sprintf("deleteFirst=%t", deleteFirst), func(t *testing.T) {
			first, second, firstControl, secondControl := newOverlappingSQLitePair(t)
			ctx := context.Background()
			seedWorkspace(t, first, "overlap-workspace")
			reserve := func(repo *Repository) error {
				return repo.CreateTaskResourceCleanupJob(ctx, &models.TaskResourceCleanupJob{
					OperationID:      models.WorkspaceDeleteAdmissionOperationID("overlap-workspace"),
					Trigger:          models.TaskResourceCleanupTriggerWorkspaceDelete,
					State:            models.TaskResourceCleanupStatePrepared,
					ResourceSnapshot: `{"workspace_id":"overlap-workspace"}`,
				})
			}
			create := func(repo *Repository) error {
				return repo.CreateTask(ctx, &models.Task{ID: "overlap-task", WorkspaceID: "overlap-workspace", Title: "Task"})
			}
			var firstErr, secondErr error
			if deleteFirst {
				firstErr, secondErr = runOverlappingTransactions(t, firstControl, secondControl,
					"SELECT id FROM workspaces", func() error { return reserve(first) }, func() error { return create(second) })
				if !errors.Is(secondErr, repoerrors.ErrTaskCleanupInProgress) {
					t.Fatalf("task creation passed workspace fence: %v", secondErr)
				}
			} else {
				firstErr, secondErr = runOverlappingTransactions(t, firstControl, secondControl,
					"SELECT id FROM workspaces", func() error { return create(first) }, func() error { return reserve(second) })
				if secondErr != nil {
					t.Fatalf("workspace fence lost creation-first race: %v", secondErr)
				}
			}
			if firstErr != nil {
				t.Fatalf("winner failed: %v", firstErr)
			}
			created, err := first.GetTask(ctx, "overlap-task")
			if deleteFirst {
				if !errors.Is(err, repoerrors.ErrTaskNotFound) || created != nil {
					t.Fatalf("delete-first race created task: %+v, %v", created, err)
				}
			} else if err != nil || created == nil || created.WorkspaceID != "overlap-workspace" {
				t.Fatalf("creation-first task missing from workspace inventory: %+v, %v", created, err)
			}
			fence, err := first.GetTaskResourceCleanupJobByOperationID(ctx,
				models.WorkspaceDeleteAdmissionOperationID("overlap-workspace"))
			if err != nil || fence == nil || fence.State != models.TaskResourceCleanupStatePrepared {
				t.Fatalf("workspace deletion fence missing: %+v, %v", fence, err)
			}
		})
	}
}
