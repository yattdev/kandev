package store

import (
	"context"
	"math"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/kandev/kandev/internal/user/models"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

func TestAccountRoleRotationInvalidatesQueueCensus(t *testing.T) {
	ctx := context.Background()
	db, err := sqlx.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	repo, err := newSQLiteRepositoryWithDB(db, db)
	require.NoError(t, err)
	_, err = db.Exec(`
		CREATE TABLE workspaces (id TEXT PRIMARY KEY, owner_id TEXT NOT NULL);
		CREATE TABLE tasks (id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL);
		CREATE TABLE task_sessions (id TEXT PRIMARY KEY, task_id TEXT NOT NULL);
		CREATE TABLE queue_session_locks (session_id TEXT PRIMARY KEY, census_revision BIGINT NOT NULL DEFAULT 0);
		INSERT INTO workspaces VALUES ('workspace', 'default-user');
		INSERT INTO tasks VALUES ('task', 'workspace');
		INSERT INTO task_sessions VALUES ('session', 'task');
		INSERT INTO queue_session_locks VALUES ('session', 0);
	`)
	require.NoError(t, err)
	for _, role := range []string{"member", "admin"} {
		_, err := repo.UpdateUserRoleStatus(ctx, DefaultUserID, role, "active")
		require.NoError(t, err)
	}
	var revision int64
	require.NoError(t, db.GetContext(ctx, &revision, `SELECT census_revision FROM queue_session_locks WHERE session_id = 'session'`))
	require.Equal(t, int64(2), revision)
	require.NoError(t, repo.SetUserOrg(ctx, DefaultUserID, "org-a"))
	require.NoError(t, repo.SetUserOrg(ctx, DefaultUserID, ""))
	require.NoError(t, repo.SetOperator(ctx, DefaultUserID, true))
	require.NoError(t, repo.SetOperator(ctx, DefaultUserID, false))
	require.NoError(t, db.GetContext(ctx, &revision, `SELECT census_revision FROM queue_session_locks WHERE session_id = 'session'`))
	require.Equal(t, int64(6), revision)
	_, err = repo.UpdateUserRoleStatus(ctx, DefaultUserID, "admin", "active")
	require.NoError(t, err)
	require.NoError(t, db.GetContext(ctx, &revision, `SELECT census_revision FROM queue_session_locks WHERE session_id = 'session'`))
	require.Equal(t, int64(6), revision)
	_, err = db.Exec(`UPDATE queue_session_locks SET census_revision = ? WHERE session_id = 'session'`, int64(math.MaxInt64))
	require.NoError(t, err)
	_, err = repo.UpdateUserRoleStatus(ctx, DefaultUserID, "member", "active")
	require.Error(t, err)
	user, err := repo.GetUser(ctx, DefaultUserID)
	require.NoError(t, err)
	require.Equal(t, "admin", user.Role)
	_, err = db.Exec(`UPDATE queue_session_locks SET census_revision = 6 WHERE session_id = 'session'`)
	require.NoError(t, err)
	_, err = repo.UpdateUserProfile(ctx, DefaultUserID, "default@kandev.local", "Default", "member")
	require.NoError(t, err)
	require.NoError(t, repo.DeleteUser(ctx, DefaultUserID))
	require.NoError(t, db.GetContext(ctx, &revision, `SELECT census_revision FROM queue_session_locks WHERE session_id = 'session'`))
	require.Equal(t, int64(8), revision)
	require.NoError(t, repo.CreateUser(ctx, &models.User{
		ID: DefaultUserID, Email: "renewed@kandev.local", DisplayName: "Renewed", Role: "admin", Status: "active",
	}))
	affected, err := repo.AssignUsersWithoutOrg(ctx, "org-a")
	require.NoError(t, err)
	require.Equal(t, int64(1), affected)
	require.NoError(t, repo.DeleteUsersByOrg(ctx, "org-a"))
	require.NoError(t, db.GetContext(ctx, &revision, `SELECT census_revision FROM queue_session_locks WHERE session_id = 'session'`))
	require.Equal(t, int64(10), revision)
}
