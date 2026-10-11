package github

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/kandev/kandev/internal/mcp/profile"
	"github.com/kandev/kandev/internal/mcp/scope"
	"github.com/kandev/kandev/internal/testutil"
	"github.com/stretchr/testify/require"
)

func scopedPRRequest() ScopedPRURLRequest {
	return ScopedPRURLRequest{WorkspaceID: "ws-1", UserID: "user", TaskID: "child", RepositoryID: "fork", Number: 42, URL: "https://github.com/upstream/project/pull/42", ExpectedHead: strings.Repeat("a", 40), SourceOwner: "fork", SourceRepo: "project"}
}

func TestScopedPRURLAdmission(t *testing.T) {
	for _, tc := range []struct {
		name, sql string
		surface   profile.Surface
	}{
		{"terminal session", `UPDATE task_sessions SET state='COMPLETED'`, profile.SurfaceKanbanTask},
		{"foreign session", `UPDATE task_sessions SET task_id='other'`, profile.SurfaceKanbanTask},
		{"not parent", `UPDATE tasks SET parent_id='other' WHERE id='child'`, profile.SurfaceKanbanTask},
		{"archived child", `UPDATE tasks SET archived_at=CURRENT_TIMESTAMP WHERE id='child'`, profile.SurfaceKanbanTask},
		{"archived parent", `UPDATE tasks SET archived_at=CURRENT_TIMESTAMP WHERE id='parent'`, profile.SurfaceKanbanTask},
		{"cross workspace", `UPDATE tasks SET workspace_id='other' WHERE id='child'`, profile.SurfaceKanbanTask},
		{"detached repo", `DELETE FROM task_repositories WHERE task_id='child'`, profile.SurfaceKanbanTask},
		{"changed source", `UPDATE repositories SET provider_owner='other'`, profile.SurfaceKanbanTask},
		{"foreign repo", `UPDATE repositories SET workspace_id='other'`, profile.SurfaceKanbanTask},
		{"config mode", `UPDATE task_sessions SET metadata='{"config_mode":true}'`, profile.SurfaceKanbanTask},
		{"managed conversation", `UPDATE tasks SET metadata='{"kandev.managed_retained":true}' WHERE id='parent'`, profile.SurfaceKanbanTask},
		{"changed origin", `UPDATE tasks SET origin='automation_run' WHERE id='parent'`, profile.SurfaceKanbanTask},
		{"automation surface", `SELECT 1`, profile.SurfaceAutomation},
		{"configuration surface", `SELECT 1`, profile.SurfaceConfiguration},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, _, s := setupPollerTest(t)
			seedScopedPRURLTasks(t, s)
			_, err := s.db.Exec(tc.sql)
			require.NoError(t, err)
			ctx := scope.WithPrincipal(context.Background(), scope.Principal{WorkspaceID: "ws-1", CallerTaskID: "parent", CallerSessionID: "session", Surface: tc.surface})
			_, _, err = s.associateScopedPRURL(ctx, scopedPRRequest(), scopedPRFixture())
			require.Error(t, err)
			rows, err := s.ListTaskPRsByTask(context.Background(), "child")
			require.NoError(t, err)
			require.Empty(t, rows)
		})
	}
}

func TestScopedPRURLCollisionRestoreAndStoredHead(t *testing.T) {
	for _, mode := range []string{"different URL", "stale stored head", "other task", "detached collision", "matching restore"} {
		t.Run(mode, func(t *testing.T) {
			_, _, _, s := setupPollerTest(t)
			seedScopedPRURLTasks(t, s)
			req := scopedPRRequest()
			pr := scopedPRFixture()
			row := &TaskPR{TaskID: req.TaskID, WorkspaceID: req.WorkspaceID, RepositoryID: req.RepositoryID, PRNumber: 42, PRURL: req.URL, Owner: "upstream", Repo: "project", HeadSHA: pr.HeadSHA, State: "merged"}
			switch mode {
			case "different URL", "detached collision":
				row.PRURL = "https://github.com/other/project/pull/42"
			case "stale stored head":
				row.HeadSHA = strings.Repeat("b", 40)
			case "other task":
				row.TaskID = "other"
			}
			require.NoError(t, s.CreateTaskPR(context.Background(), row))
			if mode == "matching restore" || mode == "detached collision" {
				_, _, err := s.DetachTaskPR(context.Background(), row.ID)
				require.NoError(t, err)
			}
			got, changed, err := s.associateScopedPRURL(scopedPRURLContext(), req, pr)
			if mode == "matching restore" {
				require.NoError(t, err)
				require.True(t, changed)
				require.Equal(t, row.ID, got.ID)
				require.Nil(t, got.DetachedAt)
			} else {
				require.Error(t, err)
				require.False(t, changed)
				saved, err := s.GetTaskPRByID(context.Background(), row.ID)
				require.NoError(t, err)
				require.Equal(t, row.PRURL, saved.PRURL)
				require.Equal(t, row.HeadSHA, saved.HeadSHA)
			}
		})
	}
}

func TestScopedPRURLConcurrentClaimsSQLite(t *testing.T) {
	db, err := sqlx.Open("sqlite3", filepath.Join(t.TempDir(), "claims.db")+"?_busy_timeout=5000&_journal_mode=WAL")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(4)
	require.NoError(t, createScopedPRPrerequisites(db, "DATETIME"))
	s, err := NewStore(db, db)
	require.NoError(t, err)
	seedScopedPRURLTasks(t, s)
	runScopedPRConcurrentClaims(t, s)
}
func TestScopedPRURLConcurrentClaimsPostgres(t *testing.T) {
	db := testutil.OpenIsolatedPostgres(t, testutil.PostgresDSNFromEnv(t))
	db.SetMaxOpenConns(4)
	require.NoError(t, createScopedPRPrerequisites(db, "TIMESTAMPTZ"))
	s, err := NewStore(db, db)
	require.NoError(t, err)
	seedScopedPRURLTasks(t, s)
	runScopedPRConcurrentClaims(t, s)
}
func createScopedPRPrerequisites(db *sqlx.DB, timestamp string) error {
	_, err := db.Exec(`CREATE TABLE workspaces(id TEXT PRIMARY KEY); CREATE TABLE tasks(id TEXT PRIMARY KEY, workspace_id TEXT, archived_at ` + timestamp + `)`)
	return err
}
func runScopedPRConcurrentClaims(t *testing.T, s *Store) {
	t.Helper()
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, taskID := range []string{"child", "other"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			req := scopedPRRequest()
			req.TaskID = taskID
			_, _, err := s.associateScopedPRURL(scopedPRURLContext(), req, scopedPRFixture())
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	passed := 0
	for err := range results {
		if err == nil {
			passed++
		}
	}
	require.Equal(t, 1, passed)
	rows, err := s.ListTaskPRsByPRNumber(context.Background(), "ws-1", "upstream", "project", 42)
	require.NoError(t, err)
	require.Len(t, rows, 1)
}

func TestScopedPRURLDifferentCanonicalNumberAndArchivedLinks(t *testing.T) {
	for _, mode := range []string{"different canonical repository", "archived competitor"} {
		t.Run(mode, func(t *testing.T) {
			_, _, _, s := setupPollerTest(t)
			seedScopedPRURLTasks(t, s)
			row := &TaskPR{TaskID: "other", WorkspaceID: "ws-1", RepositoryID: "fork", PRNumber: 42, PRURL: "https://github.com/second/project/pull/42", Owner: "second", Repo: "project", HeadSHA: strings.Repeat("a", 40), State: "open"}
			if mode == "archived competitor" {
				row.PRURL = scopedPRRequest().URL
				row.Owner = "upstream"
				_, err := s.db.Exec(`UPDATE tasks SET archived_at=CURRENT_TIMESTAMP WHERE id='other'`)
				require.NoError(t, err)
			}
			require.NoError(t, s.CreateTaskPR(context.Background(), row))
			got, changed, err := s.associateScopedPRURL(scopedPRURLContext(), scopedPRRequest(), scopedPRFixture())
			require.NoError(t, err)
			require.True(t, changed)
			require.Equal(t, "child", got.TaskID)
			saved, err := s.GetTaskPRByID(context.Background(), row.ID)
			require.NoError(t, err)
			require.Equal(t, row.PRURL, saved.PRURL)
		})
	}
}
