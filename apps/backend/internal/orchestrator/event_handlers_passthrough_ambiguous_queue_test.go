package orchestrator

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/kandev/kandev/internal/orchestrator/executor"
	"github.com/kandev/kandev/internal/orchestrator/messagequeue"
	"github.com/kandev/kandev/internal/orchestrator/watcher"
	sqliterepo "github.com/kandev/kandev/internal/task/repository/sqlite"
	sqlite3 "github.com/mattn/go-sqlite3"
)

var ambiguousQueueDriverID atomic.Uint64
var errAmbiguousQueueCommit = errors.New("commit response lost")

type ambiguousQueueFault struct {
	sync.Mutex
	commitThenError   bool
	rollbackThenError bool
	denyReads         bool
}

type ambiguousQueueDriver struct{ fault *ambiguousQueueFault }
type ambiguousQueueConn struct {
	*sqlite3.SQLiteConn
	fault *ambiguousQueueFault
}
type ambiguousQueueTx struct {
	driver.Tx
	fault *ambiguousQueueFault
}

func (d ambiguousQueueDriver) Open(name string) (driver.Conn, error) {
	conn, err := (&sqlite3.SQLiteDriver{}).Open(name)
	if err != nil {
		return nil, err
	}
	return &ambiguousQueueConn{SQLiteConn: conn.(*sqlite3.SQLiteConn), fault: d.fault}, nil
}

func (c *ambiguousQueueConn) Begin() (driver.Tx, error) {
	tx, err := c.SQLiteConn.Begin()
	if err != nil {
		return nil, err
	}
	return &ambiguousQueueTx{Tx: tx, fault: c.fault}, nil
}

func (c *ambiguousQueueConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	tx, err := c.SQLiteConn.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &ambiguousQueueTx{Tx: tx, fault: c.fault}, nil
}

func (c *ambiguousQueueConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.fault.Lock()
	denied := c.fault.denyReads
	c.fault.Unlock()
	if denied {
		return nil, errors.New("authoritative outcome read unavailable")
	}
	return c.SQLiteConn.QueryContext(ctx, query, args)
}

func (tx *ambiguousQueueTx) Commit() error {
	tx.fault.Lock()
	inject := tx.fault.commitThenError
	rollback := tx.fault.rollbackThenError
	tx.fault.commitThenError = false
	tx.fault.rollbackThenError = false
	tx.fault.Unlock()
	if rollback {
		if err := tx.Rollback(); err != nil {
			return err
		}
		return errAmbiguousQueueCommit
	}
	if err := tx.Tx.Commit(); err != nil {
		return err
	}
	if inject {
		tx.fault.Lock()
		tx.fault.denyReads = true
		tx.fault.Unlock()
		return errAmbiguousQueueCommit
	}
	return nil
}

func TestReadyPassthroughAmbiguousWorkspaceCommitCannotDispatchLateMemoryQueue(t *testing.T) {
	for _, tc := range []struct {
		name        string
		rollback    bool
		wantPrompts int
	}{
		{name: "committed_unknown", wantPrompts: 0},
		{name: "rolled_back", rollback: true, wantPrompts: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testReadyPassthroughWorkspaceCommitOutcome(t, tc.rollback, tc.wantPrompts)
		})
	}
}

func testReadyPassthroughWorkspaceCommitOutcome(t *testing.T, rollback bool, wantPrompts int) {
	ctx := context.Background()
	fault := &ambiguousQueueFault{}
	name := fmt.Sprintf("ambiguous-ready-%d", ambiguousQueueDriverID.Add(1))
	sql.Register(name, ambiguousQueueDriver{fault: fault})
	raw, err := sql.Open(name, "file:"+filepath.Join(t.TempDir(), "ready.db")+"?_foreign_keys=on&_busy_timeout=5000&_journal_mode=WAL")
	if err != nil {
		t.Fatal(err)
	}
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = raw.Close() })
	db := sqlx.NewDb(raw, "sqlite3")
	repo, err := sqliterepo.NewWithDB(db, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	seedSession(t, repo, "t1", "s1", "step1")
	seedExecutorRunning(t, repo, "s1", "t1", "exec-1")
	agentMgr := &mockAgentManager{isPassthrough: true, isAgentRunning: true, repoForExecutionLookup: repo}
	svc := createTestServiceWithAgent(repo, newMockStepGetter(), newMockTaskRepo(), agentMgr)
	svc.messageQueue = messagequeue.NewServiceMemory(testLogger())
	svc.executor = executor.NewExecutor(agentMgr, repo, testLogger(), executor.ExecutorConfig{})
	if _, err := svc.messageQueue.QueueMessage(ctx, "s1", "t1", "must not reach PTY", "", messagequeue.QueuedByAgent, false, nil); err != nil {
		t.Fatal(err)
	}
	paused, resume := make(chan struct{}), make(chan struct{})
	svc.afterReadyBeforePassthroughDispatch = func() { close(paused); <-resume }
	done := make(chan struct{})
	go func() {
		svc.handleAgentReady(ctx, watcher.AgentEventData{TaskID: "t1", SessionID: "s1"})
		close(done)
	}()
	<-paused
	fault.Lock()
	fault.commitThenError = !rollback
	fault.rollbackThenError = rollback
	fault.Unlock()
	_, _, err = repo.DeleteWorkspaceCascade(ctx, "ws1")
	if !errors.Is(err, errAmbiguousQueueCommit) {
		t.Fatalf("cascade error = %v", err)
	}
	if got := svc.messageQueue.GetStatus(ctx, "s1").Count; got != 1 {
		t.Fatalf("memory queue count during unknown outcome = %d, want 1", got)
	}
	close(resume)
	<-done
	if got := len(agentMgr.passthroughStdinCalls); got != wantPrompts {
		t.Fatalf("PTY prompts = %d, want %d", got, wantPrompts)
	}
	if !rollback {
		// Prepared-job reconciliation invokes this repository seam only after
		// it proves the mutation committed. Repeating the proof must not revive
		// or redeliver the stale process-local queue.
		repo.ResolveTaskQueuePurge(ctx, "t1", true)
		repo.ResolveTaskQueuePurge(ctx, "t1", true)
		if got := svc.messageQueue.GetStatus(ctx, "s1").Count; got != 0 {
			t.Fatalf("reconciled memory queue count = %d, want 0", got)
		}
		svc.afterReadyBeforePassthroughDispatch = nil
		svc.handleAgentReady(ctx, watcher.AgentEventData{TaskID: "t1", SessionID: "s1"})
		if got := len(agentMgr.passthroughStdinCalls); got != 0 {
			t.Fatalf("reconciled PTY prompts = %d, want 0", got)
		}
	}
}
