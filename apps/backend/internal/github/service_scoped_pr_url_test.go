package github

import (
	"context"
	"strings"
	"testing"

	"github.com/kandev/kandev/internal/mcp/profile"
	"github.com/kandev/kandev/internal/mcp/scope"
	"github.com/stretchr/testify/require"
)

func TestScopedPRURLValidation(t *testing.T) {
	pr := &PR{Number: 42, State: "merged", RepoOwner: "upstream", RepoName: "project", BaseRepoOwner: "upstream", BaseRepoName: "project", HeadRepoOwner: "fork", HeadRepoName: "project", HTMLURL: "https://github.com/upstream/project/pull/42", HeadSHA: strings.Repeat("a", 40), HeadBranch: "feature", BaseBranch: "main"}
	// The open-only contribution validator must remain unsuitable for deliverable linking.
	require.Error(t, validateGitHubContributionPR("upstream", "project", 42, pr))
	require.NoError(t, validateScopedPRURL("upstream", "project", 42, pr, "fork", "project", pr.HeadSHA))
}

func TestScopedPRURLAtomicPersistence(t *testing.T) {
	_, _, _, store := setupPollerTest(t)
	seedScopedPRURLTasks(t, store)
	req := ScopedPRURLRequest{WorkspaceID: "ws-1", UserID: "user", TaskID: "child", RepositoryID: "fork", Number: 42, URL: "https://github.com/upstream/project/pull/42", ExpectedHead: strings.Repeat("a", 40), SourceOwner: "fork", SourceRepo: "project"}
	ctx := scopedPRURLContext()
	row, changed, err := store.associateScopedPRURL(ctx, req, scopedPRFixture())
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, req.URL, row.PRURL)
	_, changed, err = store.associateScopedPRURL(ctx, req, scopedPRFixture())
	require.NoError(t, err)
	require.False(t, changed)
}

func scopedPRFixture() *PR {
	return &PR{Number: 42, State: "merged", RepoOwner: "upstream", RepoName: "project", BaseRepoOwner: "upstream", BaseRepoName: "project", HeadRepoOwner: "fork", HeadRepoName: "project", HTMLURL: "https://github.com/upstream/project/pull/42", HeadSHA: strings.Repeat("a", 40), HeadBranch: "feature", BaseBranch: "main"}
}
func scopedPRURLContext() context.Context {
	return scope.WithPrincipal(context.Background(), scope.Principal{WorkspaceID: "ws-1", CallerTaskID: "parent", CallerSessionID: "session", Surface: profile.SurfaceKanbanTask})
}
func seedScopedPRURLTasks(t *testing.T, s *Store) {
	t.Helper()
	for _, q := range []string{
		`ALTER TABLE tasks ADD COLUMN origin TEXT DEFAULT 'manual'`,
		`ALTER TABLE tasks ADD COLUMN parent_id TEXT DEFAULT ''`,
		`ALTER TABLE tasks ADD COLUMN metadata TEXT DEFAULT '{}'`,
		`CREATE TABLE task_sessions(id TEXT PRIMARY KEY,task_id TEXT,state TEXT,metadata TEXT)`,
		`CREATE TABLE repositories(id TEXT PRIMARY KEY,workspace_id TEXT,provider TEXT,provider_host TEXT,provider_owner TEXT,provider_name TEXT)`,
		`CREATE TABLE task_repositories(task_id TEXT,repository_id TEXT)`,
		`INSERT INTO tasks(id,workspace_id,parent_id) VALUES ('parent','ws-1',''),('child','ws-1','parent'),('other','ws-1','parent')`,
		`INSERT INTO task_sessions VALUES ('session','parent','RUNNING','{}')`,
		`INSERT INTO repositories VALUES ('fork','ws-1','github','github.com','fork','project')`,
		`INSERT INTO task_repositories VALUES ('child','fork'),('other','fork')`,
	} {
		_, err := s.db.Exec(q)
		require.NoError(t, err)
	}
}

func TestScopedPRURLRejectsMalformedURL(t *testing.T) {
	for _, url := range []string{"http://github.com/upstream/project/pull/42", "https://github.com.evil/upstream/project/pull/42", "https://user@github.com/upstream/project/pull/42", "https://github.com:443/upstream/project/pull/42", " https://github.com/upstream/project/pull/42", "https://github.com/upstream/project/pull/42?x=1", "https://github.com/upstream/project/pull/42#x", "https://github.com/upstream/project/pull/42//", "https://github.com//upstream/project/pull/42", "https://GITHUB.COM//upstream/project/pull/42", "https://github.com/../project/pull/42", "https://github.com/upstream/project/pull/042", "https://github.com/upstream/project/pull/0", "https://github.com/upstream/project/pull/42/x", "https://github.com/upstream%2fother/project/pull/42"} {
		t.Run(url, func(t *testing.T) { _, _, _, err := parseScopedPRURL(url); require.Error(t, err) })
	}
}
func TestScopedPRURLRejectsProviderIdentity(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*PR)
	}{
		{"missing source", func(p *PR) { p.HeadRepoOwner = "" }}, {"wrong source", func(p *PR) { p.HeadRepoName = "other" }}, {"wrong number", func(p *PR) { p.Number = 43 }},
		{"wrong target", func(p *PR) { p.BaseRepoOwner = "fork" }}, {"missing target", func(p *PR) { p.RepoName = "" }}, {"wrong URL", func(p *PR) { p.HTMLURL = "https://github.com/other/project/pull/42" }},
		{"stale head", func(p *PR) { p.HeadSHA = strings.Repeat("b", 40) }}, {"short head", func(p *PR) { p.HeadSHA = "abcdefa" }}, {"unsafe branch", func(p *PR) { p.HeadBranch = "../bad" }}, {"closed unmerged", func(p *PR) { p.State = "closed" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pr := scopedPRFixture()
			tc.change(pr)
			require.Error(t, validateScopedPRURL("upstream", "project", 42, pr, "fork", "project", strings.Repeat("a", 40)))
		})
	}
	pr := scopedPRFixture()
	pr.HeadSHA = "abcdefa"
	require.Error(t, validateScopedPRURL("upstream", "project", 42, pr, "fork", "project", "abcdefa"))
}
