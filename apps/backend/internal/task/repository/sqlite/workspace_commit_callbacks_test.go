package sqlite

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
	"github.com/kandev/kandev/internal/common/logger"
	"github.com/kandev/kandev/internal/orchestrator/messagequeue"
	sqlite3 "github.com/mattn/go-sqlite3"
)

var errCommitReceiptLost = errors.New("driver commit result lost")
var commitDriverSequence atomic.Uint64

type commitFault struct {
	sync.Mutex
	mode        string
	denyReads   bool
	deniedReads int
}
type commitDriver struct{ fault *commitFault }

func (d commitDriver) Open(name string) (driver.Conn, error) {
	c, err := (&sqlite3.SQLiteDriver{}).Open(name)
	if err != nil {
		return nil, err
	}
	return &commitConn{SQLiteConn: c.(*sqlite3.SQLiteConn), fault: d.fault}, nil
}

type commitConn struct {
	*sqlite3.SQLiteConn
	fault *commitFault
}

func (c *commitConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	tx, err := c.SQLiteConn.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &commitTx{Tx: tx, fault: c.fault}, nil
}
func (c *commitConn) Begin() (driver.Tx, error) {
	tx, err := c.SQLiteConn.Begin()
	if err != nil {
		return nil, err
	}
	return &commitTx{Tx: tx, fault: c.fault}, nil
}
func (c *commitConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	c.fault.Lock()
	denied := c.fault.denyReads
	if denied {
		c.fault.deniedReads++
	}
	c.fault.Unlock()
	if denied {
		return nil, errors.New("authoritative read unavailable")
	}
	return c.SQLiteConn.QueryContext(ctx, q, args)
}

type commitTx struct {
	driver.Tx
	fault *commitFault
}

func (tx *commitTx) Commit() error {
	tx.fault.Lock()
	mode := tx.fault.mode
	tx.fault.mode = ""
	tx.fault.Unlock()
	if mode == "rollback-error" {
		if err := tx.Rollback(); err != nil {
			return err
		}
		return errCommitReceiptLost
	}
	if err := tx.Tx.Commit(); err != nil {
		return err
	}
	if mode == "unverified-error" {
		tx.fault.Lock()
		tx.fault.denyReads = true
		tx.fault.Unlock()
	}
	if mode != "" {
		return errCommitReceiptLost
	}
	return nil
}

func TestWorkspaceCommitQueueCallbacks(t *testing.T) {
	for _, mode := range []string{"success", "committed-error", "rollback-error", "unverified-error"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			fault := &commitFault{}
			name := fmt.Sprintf("workspace-commit-%s-%d", mode, commitDriverSequence.Add(1))
			sql.Register(name, commitDriver{fault})
			raw, err := sql.Open(name, "file:"+filepath.Join(t.TempDir(), "commit.db")+"?_foreign_keys=on&_busy_timeout=5000&_journal_mode=WAL")
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
			seedWorkspaceCascadeRows(t, repo, "ws-delete")
			persistentRepo, err := messagequeue.NewSQLiteRepository(db, db)
			if err != nil {
				t.Fatal(err)
			}
			log, err := logger.NewLogger(logger.LoggingConfig{Level: "error", Format: "console"})
			if err != nil {
				t.Fatal(err)
			}
			persistent := messagequeue.NewService(persistentRepo, messagequeue.DefaultMaxPerSession, log)
			mirror := messagequeue.NewServiceMemory(log)
			for _, q := range []*messagequeue.Service{persistent, mirror} {
				_, _, accepted, err := q.QueueLifecycleMessageWithCoalesceKey(ctx, "session-delete", "task-delete", "queued lifecycle prompt", "", messagequeue.QueuedByWorkflow, false, nil, map[string]interface{}{"origin": "github_pr_automation"}, "commit-prompt", true)
				if err != nil || !accepted {
					t.Fatalf("seed queue accepted=%v error=%v", accepted, err)
				}
			}
			var purged, notified atomic.Int32
			repo.SetTaskQueuePurger(func(ctx context.Context, id string) {
				purged.Add(1)
				if _, err := mirror.PurgeTask(ctx, id); err != nil {
					t.Error(err)
				}
			})
			repo.SetTaskQueuePurgeNotifier(func(context.Context, string) { notified.Add(1) })
			if mode != "success" {
				fault.Lock()
				fault.mode = mode
				fault.Unlock()
			}
			tasks, workflows, deleteErr := repo.DeleteWorkspaceCascade(ctx, "ws-delete")
			fault.Lock()
			deniedReads := fault.deniedReads
			fault.denyReads = false
			fault.Unlock()
			if mode == "success" {
				if deleteErr != nil {
					t.Fatal(deleteErr)
				}
			} else if !errors.Is(deleteErr, errCommitReceiptLost) {
				t.Fatalf("error=%v want injected commit error", deleteErr)
			}
			workspace, workspaceErr := repo.GetWorkspace(ctx, "ws-delete")
			workspaceExists := workspaceErr == nil && workspace != nil
			durableCount := persistent.GetStatus(ctx, "session-delete").Count
			mirrorCount := mirror.GetStatus(ctx, "session-delete").Count
			t.Logf("mode=%s returnedTasks=%d workflows=%d workspaceExists=%v durableQueue=%d mirrorQueue=%d purgeCallbacks=%d notifyCallbacks=%d deniedReads=%d error=%v", mode, len(tasks), len(workflows), workspaceExists, durableCount, mirrorCount, purged.Load(), notified.Load(), deniedReads, deleteErr)
			expected := int32(0)
			if mode == "success" || mode == "committed-error" {
				expected = 1
			}
			if purged.Load() != expected || notified.Load() != expected {
				t.Errorf("postcommit callbacks purge=%d notify=%d want each %d", purged.Load(), notified.Load(), expected)
			}
			if mode == "rollback-error" {
				if !workspaceExists || durableCount != 1 || mirrorCount != 1 {
					t.Errorf("rollback state changed: workspace=%v durable=%d mirror=%d", workspaceExists, durableCount, mirrorCount)
				}
			} else {
				if workspaceExists || durableCount != 0 {
					t.Errorf("real commit did not remove durable state")
				}
			}
			if expected == 1 && mirrorCount != 0 {
				t.Errorf("committed task retained ephemeral queued prompt: %d", mirrorCount)
			}
			if mode == "unverified-error" && mirrorCount != 1 {
				t.Error("unverified result purged mirror")
			}
			if mode == "unverified-error" && deniedReads == 0 {
				t.Error("commit verification did not attempt an authoritative read")
			}
		})
	}
}
