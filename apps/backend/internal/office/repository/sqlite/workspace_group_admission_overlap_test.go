package sqlite_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"

	dbutil "github.com/kandev/kandev/internal/db"
	"github.com/kandev/kandev/internal/office/models"
	officesqlite "github.com/kandev/kandev/internal/office/repository/sqlite"
	taskmodels "github.com/kandev/kandev/internal/task/models"
	tasksqlite "github.com/kandev/kandev/internal/task/repository/sqlite"
)

// A second private writer really enters SQLite while the first transaction
// holds the write lock. After the winner commits, the loser must re-evaluate
// the durable state rather than using its pre-lock view.
func TestWorkspaceGroupMemberClaimContendedOrders(t *testing.T) {
	for _, winner := range []string{"member", "claim"} {
		t.Run(winner, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "group-overlap.db")
			open := func() *sqlx.DB {
				t.Helper()
				conn, err := dbutil.OpenSQLite(path)
				if err != nil {
					t.Fatal(err)
				}
				writer := sqlx.NewDb(conn, "sqlite3")
				t.Cleanup(func() { _ = writer.Close() })
				return writer
			}
			first := open()
			repo, err := officesqlite.NewWithDB(first, first, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := first.Exec(`CREATE TABLE IF NOT EXISTS tasks (
				id TEXT PRIMARY KEY, archived_at DATETIME
			)`); err != nil {
				t.Fatal(err)
			}
			if _, err := first.Exec(`CREATE TABLE IF NOT EXISTS executors_running (
				id TEXT PRIMARY KEY, task_id TEXT NOT NULL
			)`); err != nil {
				t.Fatal(err)
			}
			if _, err := first.Exec(`INSERT INTO tasks(id) VALUES ('live-task')`); err != nil {
				t.Fatal(err)
			}
			group := &models.WorkspaceGroup{ID: "group", WorkspaceID: "workspace",
				OwnerTaskID: "owner", MaterializedKind: models.WorkspaceGroupKindPlainFolder,
				OwnedByKandev: true,
				CleanupPolicy: models.WorkspaceCleanupPolicyDeleteWhenLastMemberArchivedOrDel}
			if err := repo.CreateWorkspaceGroup(ctx, group); err != nil {
				t.Fatal(err)
			}
			second := open()
			contender, err := officesqlite.NewWithDB(second, second, nil)
			if err != nil {
				t.Fatal(err)
			}
			tx, err := first.BeginTxx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback() }()
			if winner == "member" {
				_, err = tx.Exec(`INSERT INTO task_workspace_group_members
					(workspace_group_id, task_id, role, created_at)
					VALUES ('group', 'live-task', 'member', CURRENT_TIMESTAMP)`)
			} else {
				_, err = tx.Exec(`UPDATE task_workspace_groups SET cleanup_status = 'cleanup_pending'
					WHERE id = 'group'`)
			}
			if err != nil {
				t.Fatal(err)
			}
			// The contender must attempt a real operation while tx still owns
			// SQLite's writer lock. Shared-cache SQLite reports LOCKED here.
			contended := make(chan error, 1)
			go func() {
				if winner == "member" {
					_, err := contender.ClaimWorkspaceGroupCleanup(ctx, group.ID, group.OwnershipGeneration)
					contended <- err
				} else {
					contended <- contender.AddWorkspaceGroupMember(ctx, group.ID, "live-task", "")
				}
			}()
			err = <-contended
			if err == nil || (!strings.Contains(err.Error(), "locked") && !strings.Contains(err.Error(), "busy")) {
				t.Fatalf("contending transaction error = %v, want SQLite lock conflict", err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if winner == "member" {
				claimed, err := contender.ClaimWorkspaceGroupCleanup(ctx, group.ID, group.OwnershipGeneration)
				if err != nil || claimed {
					t.Fatalf("claim after live member = %v, %v; want false", claimed, err)
				}
			} else if err := contender.AddWorkspaceGroupMember(ctx, group.ID, "live-task", ""); err == nil {
				t.Fatal("member admitted after cleanup claim")
			}
		})
	}
}

func TestWorkspaceGroupClaimExecutorContendedOrders(t *testing.T) {
	for _, winner := range []string{"executor", "claim"} {
		t.Run(winner, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "executor-overlap.db")
			open := func() *sqlx.DB {
				t.Helper()
				conn, err := dbutil.OpenSQLite(path)
				if err != nil {
					t.Fatal(err)
				}
				writer := sqlx.NewDb(conn, "sqlite3")
				t.Cleanup(func() { _ = writer.Close() })
				return writer
			}
			first := open()
			tasks, err := tasksqlite.NewWithDB(first, first, nil)
			if err != nil {
				t.Fatal(err)
			}
			groups, err := officesqlite.NewWithDB(first, first, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := tasks.CreateWorkspace(ctx, &taskmodels.Workspace{ID: "workspace", Name: "workspace"}); err != nil {
				t.Fatal(err)
			}
			if err := tasks.CreateTask(ctx, &taskmodels.Task{ID: "task", WorkspaceID: "workspace", Title: "task"}); err != nil {
				t.Fatal(err)
			}
			if err := tasks.CreateTaskSession(ctx, &taskmodels.TaskSession{ID: "session", TaskID: "task", State: taskmodels.TaskSessionStateCompleted}); err != nil {
				t.Fatal(err)
			}
			group := &models.WorkspaceGroup{ID: "group", WorkspaceID: "workspace", OwnerTaskID: "task",
				MaterializedKind: models.WorkspaceGroupKindPlainFolder, OwnedByKandev: true,
				CleanupPolicy: models.WorkspaceCleanupPolicyDeleteWhenLastMemberArchivedOrDel}
			if err := groups.CreateWorkspaceGroup(ctx, group); err != nil {
				t.Fatal(err)
			}
			if err := groups.AddWorkspaceGroupMember(ctx, group.ID, "task", ""); err != nil {
				t.Fatal(err)
			}
			if _, err := first.Exec(`UPDATE tasks SET archived_at = CURRENT_TIMESTAMP WHERE id = 'task'`); err != nil {
				t.Fatal(err)
			}
			if err := groups.ReleaseWorkspaceGroupMember(ctx, group.ID, "task", models.WorkspaceReleaseReasonArchived, ""); err != nil {
				t.Fatal(err)
			}
			second := open()
			contenderTasks, err := tasksqlite.NewWithDB(second, second, nil)
			if err != nil {
				t.Fatal(err)
			}
			contenderGroups, err := officesqlite.NewWithDB(second, second, nil)
			if err != nil {
				t.Fatal(err)
			}
			tx, err := first.BeginTxx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback() }()
			if winner == "executor" {
				_, err = tx.Exec(`INSERT INTO executors_running
					(id, session_id, task_id, executor_id, created_at, updated_at)
					VALUES ('executor', 'session', 'task', 'agent', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
			} else {
				_, err = tx.Exec(`UPDATE task_workspace_groups SET cleanup_status = 'cleanup_pending'
					WHERE id = 'group'`)
			}
			if err != nil {
				t.Fatal(err)
			}
			contended := make(chan error, 1)
			go func() {
				if winner == "executor" {
					_, err := contenderGroups.ClaimWorkspaceGroupCleanup(ctx, group.ID, group.OwnershipGeneration)
					contended <- err
				} else {
					contended <- contenderTasks.UpsertExecutorRunning(ctx, &taskmodels.ExecutorRunning{
						ID: "executor", SessionID: "session", TaskID: "task", ExecutorID: "agent",
						Status: taskmodels.ExecutorRunningStatusRunning,
					})
				}
			}()
			err = <-contended
			if err == nil || (!strings.Contains(err.Error(), "locked") && !strings.Contains(err.Error(), "busy")) {
				t.Fatalf("contending transaction error = %v, want SQLite lock conflict", err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if winner == "executor" {
				claimed, err := contenderGroups.ClaimWorkspaceGroupCleanup(ctx, group.ID, group.OwnershipGeneration)
				if err != nil || claimed {
					t.Fatalf("claim after executor = %v, %v; want false", claimed, err)
				}
			} else if err := contenderTasks.UpsertExecutorRunning(ctx, &taskmodels.ExecutorRunning{
				ID: "executor", SessionID: "session", TaskID: "task", ExecutorID: "agent",
				Status: taskmodels.ExecutorRunningStatusRunning,
			}); err == nil {
				t.Fatal("executor admitted after cleanup claim")
			}
		})
	}
}
