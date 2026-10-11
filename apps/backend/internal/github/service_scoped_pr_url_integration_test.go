package github

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"
	taskmodels "github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

type scopedPRProbe struct {
	*MockClient
	afterRead func()
	reads     int
	writes    int
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
	for _, mode := range []string{"open", "merged", "terminal after fetch", "attachment after fetch", "provider failure", "wrong number", "stale expected", "bad URL", "readback failure", "parent after fetch", "source after fetch", "policy after fetch", "same repository", "case and slash"} {
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
			comparison := &scopedComparisonProbe{}
			svc.SetComparisonTargetObserver(comparison)
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
			case "parent after fetch":
				probe.afterRead = func() {
					_, err := store.db.Exec(`UPDATE tasks SET parent_id='other' WHERE id='child'`)
					require.NoError(t, err)
				}
			case "source after fetch":
				probe.afterRead = func() {
					_, err := store.db.Exec(`UPDATE repositories SET provider_owner='other'`)
					require.NoError(t, err)
				}
			case "policy after fetch":
				probe.afterRead = func() {
					_, err := store.db.Exec(`UPDATE task_sessions SET metadata='{"config_mode":true}'`)
					require.NoError(t, err)
				}
			case "same repository":
				req.SourceOwner = "upstream"
				pr.HeadRepoOwner = "upstream"
				_, err := store.db.Exec(`UPDATE repositories SET provider_owner='upstream'`)
				require.NoError(t, err)
			case "case and slash":
				req.ExpectedHead = strings.ToUpper(req.ExpectedHead)
				req.URL = "https://GITHUB.COM/Upstream/Project/pull/42/"
				client.SetPRDetail("Upstream", "Project", 42, pr, nil)
			case "readback failure":
				probe.afterRead = func() {
					reader, err := sqlx.Open("sqlite3", ":memory:")
					require.NoError(t, err)
					require.NoError(t, reader.Close())
					store.ro = reader
				}
			}
			row, err := svc.AssociateScopedPRURL(scopedPRURLContext(), req)
			if mode == "open" || mode == "merged" || mode == "same repository" || mode == "case and slash" {
				require.NoError(t, err)
				require.Equal(t, pr.HTMLURL, row.PRURL)
				require.Equal(t, "fork", row.RepositoryID)
				require.Equal(t, pr.HeadSHA, row.HeadSHA)
				require.Equal(t, pr.State, row.State)
				_, err = svc.AssociateScopedPRURL(scopedPRURLContext(), req)
				require.NoError(t, err)
				rows, err := store.ListTaskPRsByTask(context.Background(), req.TaskID)
				require.NoError(t, err)
				require.Len(t, rows, 1)
				require.Equal(t, 1, events.publishedCount())
				require.Equal(t, 1, comparison.calls)
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
				require.Equal(t, expected, comparison.calls)
			}
			require.Zero(t, probe.writes)
			if mode == "wrong number" || mode == "bad URL" {
				require.Zero(t, probe.reads)
			}
		})
	}
}

func (p *scopedPRProbe) SubmitReview(context.Context, string, string, int, string, string) error {
	p.writes++
	return nil
}
func (p *scopedPRProbe) RequestReviewers(context.Context, string, string, int, []string) error {
	p.writes++
	return nil
}
func (p *scopedPRProbe) MergePR(context.Context, string, string, int, MergePRRequest) (MergeOutcome, error) {
	p.writes++
	return MergeOutcomeMerged, nil
}
func (p *scopedPRProbe) CreateFork(context.Context, string, string) (*GitHubRepository, error) {
	p.writes++
	return nil, nil
}
func (p *scopedPRProbe) CreateGist(context.Context, CreateGistInput) (*GistResponse, error) {
	p.writes++
	return nil, nil
}
func (p *scopedPRProbe) DeleteGist(context.Context, string) error { p.writes++; return nil }

type scopedComparisonProbe struct{ calls int }

func (p *scopedComparisonProbe) ReconcileComparisonTarget(context.Context, string, taskmodels.ComparisonTargetCandidate) (*taskmodels.ComparisonTargetReconciliation, error) {
	p.calls++
	return nil, nil
}
func (p *scopedComparisonProbe) ReconcileComparisonTargetFromSync(ctx context.Context, id string, c taskmodels.ComparisonTargetCandidate) (*taskmodels.ComparisonTargetReconciliation, error) {
	return p.ReconcileComparisonTarget(ctx, id, c)
}

func (p *scopedComparisonProbe) RemoveComparisonTargetForChange(context.Context, string, string, string, string, int) error {
	p.calls++
	return nil
}
