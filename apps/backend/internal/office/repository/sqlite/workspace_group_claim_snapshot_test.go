package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/jmoiron/sqlx"

	dbutil "github.com/kandev/kandev/internal/db"
	"github.com/kandev/kandev/internal/office/models"
)

func TestWorkspaceGroupAdmissionTxOptions(t *testing.T) {
	conn, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	pg := &Repository{db: sqlx.NewDb(conn, "pgx")}
	if opts := pg.workspaceGroupAdmissionTxOptions(); opts == nil || opts.Isolation != sql.LevelReadCommitted {
		t.Fatalf("PostgreSQL group admission isolation = %+v, want READ COMMITTED", opts)
	}
	sqlite := &Repository{db: sqlx.NewDb(conn, "sqlite3")}
	if opts := sqlite.workspaceGroupAdmissionTxOptions(); opts != nil {
		t.Fatalf("SQLite group admission options = %+v, want default writer semantics", opts)
	}
}

func newClaimSnapshotTestRepo(t *testing.T) *Repository {
	t.Helper()
	conn, err := dbutil.OpenSQLite(filepath.Join(t.TempDir(), "claim-snapshot.db"))
	if err != nil {
		t.Fatal(err)
	}
	writer := sqlx.NewDb(conn, "sqlite3")
	t.Cleanup(func() { _ = writer.Close() })
	repo, err := NewWithDB(writer, writer, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Exec(`CREATE TABLE tasks (
		id TEXT PRIMARY KEY, archived_at TIMESTAMP
	); CREATE TABLE executors_running (
		id TEXT PRIMARY KEY, task_id TEXT NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Exec(`INSERT INTO tasks(id, archived_at) VALUES ('new-member', CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	group := &models.WorkspaceGroup{ID: "snapshot-group", WorkspaceID: "workspace",
		OwnerTaskID: "owner", MaterializedKind: models.WorkspaceGroupKindPlainFolder,
		OwnedByKandev: true, CleanupPolicy: models.WorkspaceCleanupPolicyDeleteWhenLastMemberArchivedOrDel}
	if err := repo.CreateWorkspaceGroup(context.Background(), group); err != nil {
		t.Fatal(err)
	}
	return repo
}

func TestWorkspaceGroupClaimMemberSetChangeRollsBackAndRetries(t *testing.T) {
	ctx := context.Background()
	repo := newClaimSnapshotTestRepo(t)
	attempts := 0
	claimed, err := repo.claimWorkspaceGroupCleanup(ctx, "snapshot-group", 1,
		func(tx *sqlx.Tx, attempt int) error {
			attempts++
			if attempt != 0 {
				return nil
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO task_workspace_group_members
				(workspace_group_id, task_id, role, created_at)
				VALUES ('snapshot-group', 'new-member', 'member', CURRENT_TIMESTAMP)`)
			return err
		})
	if err != nil || !claimed || attempts != 2 {
		t.Fatalf("changed member set claim = %v, %v, attempts=%d; want retry then claim", claimed, err, attempts)
	}
	var members int
	if err := repo.db.GetContext(ctx, &members,
		`SELECT COUNT(*) FROM task_workspace_group_members WHERE workspace_group_id = 'snapshot-group'`); err != nil {
		t.Fatal(err)
	}
	if members != 0 {
		t.Fatalf("first attempt's inserted member persisted: %d", members)
	}
}

func TestWorkspaceGroupClaimMemberSetRetryExhaustionDenies(t *testing.T) {
	ctx := context.Background()
	repo := newClaimSnapshotTestRepo(t)
	attempts := 0
	claimed, err := repo.claimWorkspaceGroupCleanup(ctx, "snapshot-group", 1,
		func(tx *sqlx.Tx, _ int) error {
			attempts++
			_, err := tx.ExecContext(ctx, `INSERT INTO task_workspace_group_members
				(workspace_group_id, task_id, role, created_at)
				VALUES ('snapshot-group', 'new-member', 'member', CURRENT_TIMESTAMP)`)
			return err
		})
	if !errors.Is(err, errWorkspaceGroupMemberSetChanged) || claimed || attempts != 3 {
		t.Fatalf("unstable member set claim = %v, %v, attempts=%d; want bounded denial", claimed, err, attempts)
	}
	group, err := repo.GetWorkspaceGroup(ctx, "snapshot-group")
	if err != nil || group == nil || group.CleanupStatus != models.WorkspaceCleanupStatusActive {
		t.Fatalf("exhausted claim altered group: %+v, %v", group, err)
	}
	var members int
	if err := repo.db.GetContext(ctx, &members,
		`SELECT COUNT(*) FROM task_workspace_group_members WHERE workspace_group_id = 'snapshot-group'`); err != nil {
		t.Fatal(err)
	}
	if members != 0 {
		t.Fatalf("exhausted attempt's inserted member persisted: %d", members)
	}
}
