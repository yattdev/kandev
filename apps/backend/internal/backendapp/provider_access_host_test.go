package backendapp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/github"
	"github.com/kandev/kandev/internal/provideraccess"
	"github.com/kandev/kandev/pkg/pluginsdk"
)

type hostTestTokens struct {
	mints, revokes int
	revokeErr      error
}

func (f *hostTestTokens) Mint(_ context.Context, installationID int64,
	repository string) (github.InstallationToken, error) {
	f.mints++
	return github.InstallationToken{Token: "fixture-bearer", ExpiresAt: time.Now().Add(30 * time.Minute),
		Principal: github.TokenPrincipal{Kind: github.TokenCredentialInstallation,
			PrincipalID: "installation:42", InstallationID: installationID},
		Repositories: []github.InstallationTokenRepository{{FullName: repository}},
		Permissions: github.InstallationPermissions{"actions": github.PermissionWrite,
			"pull_requests": github.PermissionRead, "metadata": github.PermissionRead}}, nil
}

func (f *hostTestTokens) Revoke(context.Context, string) error {
	f.revokes++
	return f.revokeErr
}

func hostTestSpec(target provideraccess.GitHubRerunTarget,
	grant provideraccess.Grant) pluginsdk.ProviderAccessLeaseSpec {
	return pluginsdk.ProviderAccessLeaseSpec{RequestID: "request-1", IdempotencyKey: "idempotency-1",
		GrantID: grant.ID, WorkspaceID: grant.WorkspaceID, ManagedTaskID: "managed-task-1",
		SessionID: "session-1", TargetTaskID: grant.TargetTaskID,
		RepositoryID: grant.RepositoryID, Provider: grant.Provider, Purpose: grant.Purpose,
		Target: pluginsdk.ProviderAccessGitHubRerunTarget{Operation: target.Operation,
			PRNumber: int32(target.PRNumber), BaseRepositoryID: target.BaseRepositoryID,
			BaseRepository: target.BaseRepository, BaseRef: target.BaseRef, BaseSHA: target.BaseSHA,
			HeadRepositoryID: target.HeadRepositoryID, HeadRepository: target.HeadRepository,
			HeadRef: target.HeadRef, HeadSHA: target.HeadSHA, SourceRunID: target.SourceRunID,
			SourceAttempt: int32(target.SourceAttempt), WorkflowID: target.WorkflowID}}
}

func TestProviderHostAccessBindsPluginAndExactGrantBeforeOneShotRedemption(t *testing.T) {
	authority, target, _, _, _, _ := newLeaseAuthorityFixture(t)
	ctx := context.Background()
	grant, err := authority.store.GetGrant(ctx, "grant-1")
	if err != nil || grant == nil {
		t.Fatalf("grant = %+v, err = %v", grant, err)
	}
	tokens := &hostTestTokens{}
	host := &providerHostAccess{store: authority.store, grants: authority.grants,
		managed: authority.managed, provider: authority.provider, tokens: tokens}
	spec := hostTestSpec(target, *grant)
	wrongScopes := map[string]func(*pluginsdk.ProviderAccessLeaseSpec){
		"workspace":   func(s *pluginsdk.ProviderAccessLeaseSpec) { s.WorkspaceID = "foreign-workspace" },
		"target task": func(s *pluginsdk.ProviderAccessLeaseSpec) { s.TargetTaskID = "foreign-task" },
		"repository":  func(s *pluginsdk.ProviderAccessLeaseSpec) { s.RepositoryID = "foreign-repo" },
		"provider":    func(s *pluginsdk.ProviderAccessLeaseSpec) { s.Provider = "gitlab" },
		"purpose":     func(s *pluginsdk.ProviderAccessLeaseSpec) { s.Purpose = "admin" },
	}
	for name, change := range wrongScopes {
		t.Run(name, func(t *testing.T) {
			wrong := spec
			change(&wrong)
			if _, err := host.Issue(ctx, grant.PluginID, wrong); !errors.Is(err, provideraccess.ErrGrantUnavailable) {
				t.Fatalf("changed scope error = %v", err)
			}
		})
	}
	if _, err := host.Issue(ctx, "foreign-plugin", spec); !errors.Is(err, provideraccess.ErrGrantUnavailable) {
		t.Fatalf("foreign plugin error = %v", err)
	}
	lease, err := host.Issue(ctx, grant.PluginID, spec)
	if err != nil || lease.LeaseID == "" || lease.CanonicalRepository != target.BaseRepository {
		t.Fatalf("exact lease = %+v, err = %v", lease, err)
	}
	if _, err := host.Redeem(ctx, "foreign-plugin", "request-2", lease.LeaseID); !errors.Is(err, provideraccess.ErrGrantUnavailable) {
		t.Fatalf("foreign redemption error = %v", err)
	}
	credential, err := host.Redeem(ctx, grant.PluginID, "request-2", lease.LeaseID)
	if err != nil || credential.Bearer() != "fixture-bearer" || tokens.mints != 1 {
		t.Fatalf("redemption err = %v, mints = %d", err, tokens.mints)
	}
	if _, err := host.Redeem(ctx, grant.PluginID, "request-3", lease.LeaseID); !errors.Is(err, provideraccess.ErrLeaseAlreadyRedeemed) {
		t.Fatalf("replay error = %v", err)
	}
	if err := authority.store.RevokeGrant(ctx, grant.WorkspaceID, grant.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := host.Release(ctx, "foreign-plugin", "request-4", lease.LeaseID); !errors.Is(err, provideraccess.ErrGrantUnavailable) {
		t.Fatalf("foreign release error = %v", err)
	}
	revoked, err := host.Release(ctx, grant.PluginID, "request-5", lease.LeaseID)
	if err != nil || !revoked || tokens.revokes != 1 {
		t.Fatalf("owner release = %v, err = %v, revokes = %d", revoked, err, tokens.revokes)
	}
	if err := host.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := host.Issue(ctx, grant.PluginID, spec); !errors.Is(err, provideraccess.ErrGrantUnavailable) {
		t.Fatalf("stopped Host issue error = %v", err)
	}
}

func TestProviderHostAccessAdminRevocationRevokesExportedToken(t *testing.T) {
	authority, target, _, _, _, _ := newLeaseAuthorityFixture(t)
	ctx := context.Background()
	grant, err := authority.store.GetGrant(ctx, "grant-1")
	if err != nil || grant == nil {
		t.Fatalf("grant = %+v, err = %v", grant, err)
	}
	tokens := &hostTestTokens{}
	host := &providerHostAccess{store: authority.store, grants: authority.grants,
		managed: authority.managed, provider: authority.provider, tokens: tokens}
	spec := hostTestSpec(target, *grant)
	lease, err := host.Issue(ctx, grant.PluginID, spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Redeem(ctx, grant.PluginID, "redeem-1", lease.LeaseID); err != nil {
		t.Fatal(err)
	}
	if err := host.RevokeGrant(ctx, grant.WorkspaceID, grant.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if tokens.revokes != 1 {
		t.Fatalf("revoke calls = %d, want one", tokens.revokes)
	}
	state, err := authority.store.ExposureStateAt(ctx, lease.LeaseID, time.Now().UTC())
	if err != nil || state != provideraccess.ExposureRevokedAtProvider {
		t.Fatalf("exposure state = %s, err = %v", state, err)
	}
}

func TestProviderHostAccessReplacementRetainsFailedRevocationResidual(t *testing.T) {
	authority, target, _, _, _, _ := newLeaseAuthorityFixture(t)
	ctx := context.Background()
	grant, err := authority.store.GetGrant(ctx, "grant-1")
	if err != nil || grant == nil {
		t.Fatalf("grant = %+v, err = %v", grant, err)
	}
	tokens := &hostTestTokens{revokeErr: errors.New("provider fixture unavailable")}
	host := &providerHostAccess{store: authority.store, grants: authority.grants,
		managed: authority.managed, provider: authority.provider, tokens: tokens}
	lease, err := host.Issue(ctx, grant.PluginID, hostTestSpec(target, *grant))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Redeem(ctx, grant.PluginID, "redeem-1", lease.LeaseID); err != nil {
		t.Fatal(err)
	}
	successor := *grant
	successor.ID = "grant-successor"
	if err := host.ReplaceGrant(ctx, &successor); !errors.Is(err, provideraccess.ErrRevocationUnconfirmed) {
		t.Fatalf("failed replacement error = %v", err)
	}
	if tokens.revokes != 1 {
		t.Fatalf("revoke calls = %d, want one", tokens.revokes)
	}
	active, err := authority.store.GetActiveGrant(ctx, grant.Scope())
	if err != nil || active != nil {
		t.Fatalf("active grant after failed revocation = %+v, err = %v", active, err)
	}
	state, err := authority.store.ExposureStateAt(ctx, lease.LeaseID, time.Now().UTC())
	if err != nil || state != provideraccess.ExposureResidual {
		t.Fatalf("failed revocation state = %s, err = %v", state, err)
	}
}

func TestProviderHostAccessWorkspaceCleanupFencesAndRevokes(t *testing.T) {
	authority, target, _, _, _, _ := newLeaseAuthorityFixture(t)
	ctx := context.Background()
	grant, err := authority.store.GetGrant(ctx, "grant-1")
	if err != nil || grant == nil {
		t.Fatalf("grant = %+v, err = %v", grant, err)
	}
	tokens := &hostTestTokens{}
	host := &providerHostAccess{store: authority.store, grants: authority.grants,
		managed: authority.managed, provider: authority.provider, tokens: tokens}
	lease, err := host.Issue(ctx, grant.PluginID, hostTestSpec(target, *grant))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Redeem(ctx, grant.PluginID, "redeem-1", lease.LeaseID); err != nil {
		t.Fatal(err)
	}
	if err := host.CleanupWorkspaceProviderAccess(ctx, grant.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	if tokens.revokes != 1 {
		t.Fatalf("workspace cleanup revocations = %d", tokens.revokes)
	}
	state, err := authority.store.ExposureStateAt(ctx, lease.LeaseID, time.Now().UTC())
	if err != nil || state != provideraccess.ExposureRevokedAtProvider {
		t.Fatalf("workspace cleanup state = %s, err = %v", state, err)
	}
	successor := *grant
	successor.ID = "grant-successor"
	if err := host.ReplaceGrant(ctx, &successor); !errors.Is(err, provideraccess.ErrGrantUnavailable) {
		t.Fatalf("fenced workspace replacement error = %v", err)
	}
}

func TestProviderHostAccessPluginStopRevokesTokenAndGrant(t *testing.T) {
	authority, target, _, _, _, _ := newLeaseAuthorityFixture(t)
	ctx := context.Background()
	grant, err := authority.store.GetGrant(ctx, "grant-1")
	if err != nil || grant == nil {
		t.Fatalf("grant = %+v, err = %v", grant, err)
	}
	tokens := &hostTestTokens{}
	host := &providerHostAccess{store: authority.store, grants: authority.grants,
		managed: authority.managed, provider: authority.provider, tokens: tokens}
	lease, err := host.Issue(ctx, grant.PluginID, hostTestSpec(target, *grant))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Redeem(ctx, grant.PluginID, "redeem-1", lease.LeaseID); err != nil {
		t.Fatal(err)
	}
	if err := host.StopPlugin(ctx, grant.PluginID); err != nil {
		t.Fatal(err)
	}
	if tokens.revokes != 1 {
		t.Fatalf("plugin stop revocations = %d", tokens.revokes)
	}
	active, err := authority.store.GetActiveGrant(ctx, grant.Scope())
	if err != nil || active != nil {
		t.Fatalf("plugin stop active grant = %+v, err = %v", active, err)
	}
	if err := host.StopPlugin(ctx, grant.PluginID); err != nil {
		t.Fatalf("repeat plugin stop: %v", err)
	}
}

func TestProviderHostAccessPluginStopFailureBlocksNewRuntimeUntilRetry(t *testing.T) {
	authority, target, _, _, _, _ := newLeaseAuthorityFixture(t)
	ctx := context.Background()
	grant, err := authority.store.GetGrant(ctx, "grant-1")
	if err != nil || grant == nil {
		t.Fatalf("grant = %+v, err = %v", grant, err)
	}
	tokens := &hostTestTokens{revokeErr: errors.New("provider fixture unavailable")}
	host := &providerHostAccess{store: authority.store, grants: authority.grants,
		managed: authority.managed, provider: authority.provider, tokens: tokens}
	lease, err := host.Issue(ctx, grant.PluginID, hostTestSpec(target, *grant))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Redeem(ctx, grant.PluginID, "redeem-1", lease.LeaseID); err != nil {
		t.Fatal(err)
	}
	if err := host.StopPlugin(ctx, grant.PluginID); !errors.Is(err, provideraccess.ErrRevocationUnconfirmed) {
		t.Fatalf("failed stop error = %v", err)
	}
	if _, err := host.runtime(grant.PluginID); !errors.Is(err, provideraccess.ErrGrantUnavailable) {
		t.Fatalf("blocked runtime error = %v", err)
	}
	tokens.revokeErr = nil
	if err := host.StopPlugin(ctx, grant.PluginID); err != nil {
		t.Fatalf("retry stop error = %v", err)
	}
	if _, err := host.runtime(grant.PluginID); err != nil {
		t.Fatalf("runtime after confirmed revocation = %v", err)
	}
}

func TestProviderHostAccessSessionTeardownFencesExportedToken(t *testing.T) {
	authority, target, _, _, _, _ := newLeaseAuthorityFixture(t)
	ctx := context.Background()
	grant, err := authority.store.GetGrant(ctx, "grant-1")
	if err != nil || grant == nil {
		t.Fatalf("grant = %+v, err = %v", grant, err)
	}
	tokens := &hostTestTokens{}
	host := &providerHostAccess{store: authority.store, grants: authority.grants,
		managed: authority.managed, provider: authority.provider, tokens: tokens}
	lease, err := host.Issue(ctx, grant.PluginID, hostTestSpec(target, *grant))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Redeem(ctx, grant.PluginID, "redeem-1", lease.LeaseID); err != nil {
		t.Fatal(err)
	}
	if err := host.RevokeSession(ctx, "session-1"); err != nil {
		t.Fatal(err)
	}
	if tokens.revokes != 1 {
		t.Fatalf("session teardown revocations = %d", tokens.revokes)
	}
	active, err := authority.store.GetActiveLease(ctx, lease.LeaseID)
	if err != nil || active != nil {
		t.Fatalf("session teardown active lease = %+v, err = %v", active, err)
	}
}
