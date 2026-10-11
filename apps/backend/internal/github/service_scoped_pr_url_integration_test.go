package github

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"
)

type scopedPRProbe struct {
	*MockClient
	afterRead func()
	reads     int
}

func (p *scopedPRProbe) GetPR(ctx context.Context, owner, repo string, number int) (*PR, error) {
	p.reads++
	row, err := p.MockClient.GetPR(ctx, owner, repo, number)
	if p.afterRead != nil {
		p.afterRead()
	}
	return row, err
}

func TestScopedPRURLProviderAndStoreIntegration(t *testing.T) {
	for _, mode := range []string{"open", "merged", "terminal after fetch", "attachment after fetch", "provider failure", "wrong number", "stale expected", "bad URL", "readback failure"} {
		t.Run(mode, func(t *testing.T) {
			_, svc, client, store := setupPollerTest(t)
			seedScopedPRURLTasks(t, store)
			events := &mockEventBus{}
			svc.eventBus = events
			req := scopedPRRequest()
			pr := scopedPRFixture()
			client.AddRepos("upstream", []GitHubRepo{{Owner: "upstream", Name: "project", FullName: "upstream/project"}})
			client.SetRepositoryDetails(GitHubRepository{Owner: "upstream", Name: "project", FullName: "upstream/project"})
			client.SetPRDetail("upstream", "project", 42, pr, nil)
			probe := &scopedPRProbe{MockClient: client}
			configureTestWorkspaceAuth(t, svc, probe, "ws-1")
			switch mode {
			case "open":
				pr.State = "open"
			case "terminal after fetch":
				probe.afterRead = func() { _, err := store.db.Exec(`UPDATE task_sessions SET state='COMPLETED'`); require.NoError(t, err) }
			case "attachment after fetch":
				probe.afterRead = func() {
					_, err := store.db.Exec(`DELETE FROM task_repositories WHERE task_id='child'`)
					require.NoError(t, err)
				}
			case "provider failure":
				client.SetPRDetail("upstream", "project", 42, nil, sql.ErrConnDone)
			case "wrong number":
				req.Number = 43
			case "stale expected":
				req.ExpectedHead = strings.Repeat("b", 40)
			case "bad URL":
				req.URL = "https://github.com/upstream/project/pull/42?x=1"
			case "readback failure":
				probe.afterRead = func() {
					reader, err := sqlx.Open("sqlite3", ":memory:")
					require.NoError(t, err)
					require.NoError(t, reader.Close())
					store.ro = reader
				}
			}
			row, err := svc.AssociateScopedPRURL(scopedPRURLContext(), req)
			if mode == "open" || mode == "merged" {
				require.NoError(t, err)
				require.Equal(t, req.URL, row.PRURL)
				require.Equal(t, "fork", row.RepositoryID)
				require.Equal(t, req.ExpectedHead, row.HeadSHA)
				require.Equal(t, pr.State, row.State)
				_, err = svc.AssociateScopedPRURL(scopedPRURLContext(), req)
				require.NoError(t, err)
				rows, err := store.ListTaskPRsByTask(context.Background(), req.TaskID)
				require.NoError(t, err)
				require.Len(t, rows, 1)
				require.Equal(t, 1, events.publishedCount())
			} else {
				require.Error(t, err)
				if mode == "readback failure" {
					require.ErrorIs(t, err, ErrScopedPRURLReadback)
				}
				var count int
				require.NoError(t, store.db.Get(&count, `SELECT COUNT(*) FROM github_task_prs`))
				expected := 0
				if mode == "readback failure" {
					expected = 1
				}
				require.Equal(t, expected, count)
				require.Equal(t, expected, events.publishedCount())
			}
			if mode == "wrong number" || mode == "bad URL" {
				require.Zero(t, probe.reads)
			}
		})
	}
}
