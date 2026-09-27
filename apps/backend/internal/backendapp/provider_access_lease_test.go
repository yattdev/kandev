package backendapp

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/kandev/kandev/internal/github"
	"github.com/kandev/kandev/internal/provideraccess"
	_ "github.com/mattn/go-sqlite3"
)

type leaseManagedStub struct{ live bool }

func (s *leaseManagedStub) VerifyManagedSession(context.Context, string, string, string, string, string) (bool, error) {
	return s.live, nil
}

type leaseGitHubStub struct {
	link *github.TaskPR
	pr   *github.PR
	run  github.WorkflowRun
}

func (s *leaseGitHubStub) GetTaskPRByOwnerRepoNumber(context.Context, string, string, string, int) (*github.TaskPR, error) {
	return s.link, nil
}

func (s *leaseGitHubStub) GetPRForAutomation(context.Context, string, string, string, int) (*github.PR, error) {
	return s.pr, nil
}

func (s *leaseGitHubStub) ListWorkflowRunsForAutomation(context.Context, string, string, string, string) ([]github.WorkflowRun, error) {
	return []github.WorkflowRun{s.run}, nil
}

func newLeaseAuthorityFixture(t *testing.T) (*providerLeaseAuthority, provideraccess.GitHubRerunTarget,
	*leaseManagedStub, *leaseGitHubStub, *grantPluginReaderStub, *grantConnectionReaderStub) {
	t.Helper()
	db, err := sqlx.Open("sqlite3", filepath.Join(t.TempDir(), "provider-access.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store, err := provideraccess.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	grants, scope, _, plugins, connections := newGrantAuthorityFixture()
	connections.connection.CredentialGeneration = 1
	grant := provideraccess.Grant{ID: "grant-1", GrantScope: scope,
		CreatedByUserID: "admin-1", ExpiresAt: time.Now().UTC().Add(time.Hour)}
	grant.PluginInstallationID = "plugin-installation-1"
	if err := store.ReplaceGrant(context.Background(), &grant); err != nil {
		t.Fatal(err)
	}
	target := provideraccess.GitHubRerunTarget{Operation: "rerun_failed_jobs", PRNumber: 3165,
		BaseRepositoryID: 10, BaseRepository: "owner/repo", BaseRef: "main",
		BaseSHA: strings.Repeat("a", 40), HeadRepositoryID: 20,
		HeadRepository: "contributor/fork", HeadRef: "feature", HeadSHA: strings.Repeat("b", 40),
		SourceRunID: 77, SourceAttempt: 2, WorkflowID: 55}
	provider := &leaseGitHubStub{link: &github.TaskPR{WorkspaceID: scope.WorkspaceID,
		TaskID: scope.TargetTaskID, RepositoryID: scope.RepositoryID, Owner: "owner", Repo: "repo",
		PRNumber: target.PRNumber, HeadSHA: target.HeadSHA, State: providerPRStateOpen},
		pr: &github.PR{Number: target.PRNumber, State: providerPRStateOpen, BaseRepoID: 10,
			BaseRepoOwner: "owner", BaseRepoName: "repo", BaseBranch: "main", BaseSHA: target.BaseSHA,
			HeadRepoID: 20, HeadRepoOwner: "contributor", HeadRepoName: "fork",
			HeadBranch: "feature", HeadSHA: target.HeadSHA},
		run: github.WorkflowRun{ID: 77, RunAttempt: 2, WorkflowID: 55, Event: "pull_request",
			Status: "completed", Conclusion: "failure", HeadSHA: target.HeadSHA, HeadBranch: "feature",
			HeadRepoID: 20, HeadRepoOwner: "contributor", HeadRepoName: "fork"}}
	managed := &leaseManagedStub{live: true}
	authority := &providerLeaseAuthority{store: store, grants: grants,
		managed: managed, provider: provider, pluginID: scope.PluginID}
	return authority, target, managed, provider, plugins, connections
}

func TestProviderLeaseAuthorityIssuesAndRechecksCurrentExactTarget(t *testing.T) {
	authority, target, managed, provider, plugins, connection := newLeaseAuthorityFixture(t)
	ctx := context.Background()
	lease, err := authority.Issue(ctx, "grant-1", "managed-task-1", "session-1", target, "request-1")
	if err != nil {
		t.Fatal(err)
	}
	replay, err := authority.Issue(ctx, "grant-1", "managed-task-1", "session-1", target, "request-1")
	if err != nil || replay.ID != lease.ID {
		t.Fatalf("idempotent lease replay = %+v, err = %v", replay, err)
	}
	verified, err := authority.VerifyLease(ctx, lease.ID)
	if err != nil || verified.LeaseID != lease.ID || verified.CanonicalRepository != "owner/repo" ||
		verified.AppRegistrationID != "app-registration-1" {
		t.Fatalf("verified = %+v, err = %v", verified, err)
	}
	checks := []struct {
		name          string
		breakIdentity func()
		restore       func()
	}{
		{"managed session", func() { managed.live = false }, func() { managed.live = true }},
		{"PR head", func() { provider.pr.HeadSHA = strings.Repeat("c", 40) }, func() { provider.pr.HeadSHA = target.HeadSHA }},
		{"run attempt", func() { provider.run.RunAttempt++ }, func() { provider.run.RunAttempt-- }},
		{"H6 approval", func() { plugins.allowed = false }, func() { plugins.allowed = true }},
		{"approval revision", func() { plugins.approval.Revision++ }, func() { plugins.approval.Revision-- }},
		{"App generation", func() { connection.connection.CredentialGeneration++ }, func() { connection.connection.CredentialGeneration-- }},
		{"App registration", func() { connection.connection.AppRegistrationID = "foreign-registration" },
			func() { connection.connection.AppRegistrationID = "app-registration-1" }},
		{"App installation", func() { *connection.connection.InstallationID++ }, func() { *connection.connection.InstallationID-- }},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			check.breakIdentity()
			defer check.restore()
			if _, err := authority.VerifyLease(ctx, lease.ID); !errors.Is(err, provideraccess.ErrGrantUnavailable) {
				t.Fatalf("drift error = %v", err)
			}
		})
	}
}

func TestProviderLeaseAuthorityDeniesRevokedGrantBeforeRedemption(t *testing.T) {
	authority, target, _, _, _, _ := newLeaseAuthorityFixture(t)
	ctx := context.Background()
	lease, err := authority.Issue(ctx, "grant-1", "managed-task-1", "session-1", target, "request-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := authority.store.RevokeGrant(ctx, "workspace-1", "grant-1", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := authority.VerifyLease(ctx, lease.ID); !errors.Is(err, provideraccess.ErrGrantUnavailable) {
		t.Fatalf("revoked grant verification = %v", err)
	}
}

func TestProviderLeaseAuthorityDeniesWrongConnectionAndProviderSelectors(t *testing.T) {
	cases := map[string]func(*providerLeaseAuthority, *provideraccess.GitHubRerunTarget,
		*leaseManagedStub, *leaseGitHubStub, *grantPluginReaderStub, *grantConnectionReaderStub){
		"foreign plugin connection": func(a *providerLeaseAuthority, _ *provideraccess.GitHubRerunTarget,
			_ *leaseManagedStub, _ *leaseGitHubStub, _ *grantPluginReaderStub, _ *grantConnectionReaderStub) {
			a.pluginID = "other-plugin"
		},
		"stale session": func(_ *providerLeaseAuthority, _ *provideraccess.GitHubRerunTarget,
			m *leaseManagedStub, _ *leaseGitHubStub, _ *grantPluginReaderStub, _ *grantConnectionReaderStub) {
			m.live = false
		},
		"wrong base": func(_ *providerLeaseAuthority, target *provideraccess.GitHubRerunTarget,
			_ *leaseManagedStub, _ *leaseGitHubStub, _ *grantPluginReaderStub, _ *grantConnectionReaderStub) {
			target.BaseRepository = "other/repo"
		},
		"wrong fork": func(_ *providerLeaseAuthority, target *provideraccess.GitHubRerunTarget,
			_ *leaseManagedStub, _ *leaseGitHubStub, _ *grantPluginReaderStub, _ *grantConnectionReaderStub) {
			target.HeadRepositoryID++
		},
		"wrong attempt": func(_ *providerLeaseAuthority, target *provideraccess.GitHubRerunTarget,
			_ *leaseManagedStub, _ *leaseGitHubStub, _ *grantPluginReaderStub, _ *grantConnectionReaderStub) {
			target.SourceAttempt++
		},
		"unapproved": func(_ *providerLeaseAuthority, _ *provideraccess.GitHubRerunTarget,
			_ *leaseManagedStub, _ *leaseGitHubStub, p *grantPluginReaderStub, _ *grantConnectionReaderStub) {
			p.allowed = false
		},
		"PAT connection": func(_ *providerLeaseAuthority, _ *provideraccess.GitHubRerunTarget,
			_ *leaseManagedStub, _ *leaseGitHubStub, _ *grantPluginReaderStub, c *grantConnectionReaderStub) {
			c.connection.Source = github.ConnectionSourcePAT
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			a, target, managed, provider, plugins, connection := newLeaseAuthorityFixture(t)
			mutate(a, &target, managed, provider, plugins, connection)
			if _, err := a.Issue(context.Background(), "grant-1", "managed-task-1", "session-1", target, "request-1"); !errors.Is(err, provideraccess.ErrGrantUnavailable) {
				t.Fatalf("issued unauthorized lease: %v", err)
			}
		})
	}
}
