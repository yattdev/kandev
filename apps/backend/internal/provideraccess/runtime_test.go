package provideraccess

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/github"
)

type fakeLeaseAuthority struct {
	mu       sync.Mutex
	verified VerifiedLease
	drift    bool
	calls    int
}

func (a *fakeLeaseAuthority) VerifyLease(_ context.Context, _ string) (VerifiedLease, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls++
	result := a.verified
	if a.drift && a.calls > 1 {
		result.Expected.SessionID = "other-session"
	}
	return result, nil
}

type fakeRerunTokens struct {
	mu          sync.Mutex
	mintCount   int
	revocations int
	revokeErr   error
	onMint      func()
	overbroad   bool
	repository  string
}

func (f *fakeRerunTokens) Mint(_ context.Context, _ string, _ int64, _ string) (github.InstallationToken, error) {
	f.mu.Lock()
	f.mintCount++
	onMint := f.onMint
	f.mu.Unlock()
	if onMint != nil {
		onMint()
	}
	token := github.InstallationToken{Token: "fake-secret",
		ExpiresAt: time.Now().Add(45 * time.Minute),
		Principal: github.TokenPrincipal{Kind: github.TokenCredentialInstallation,
			PrincipalID: "installation:42", InstallationID: 42},
		Permissions: github.InstallationPermissions{
			"actions": github.PermissionWrite, "pull_requests": github.PermissionRead,
			"metadata": github.PermissionRead,
		},
		Repositories: []github.InstallationTokenRepository{{FullName: f.repositoryName()}}}
	if f.overbroad {
		token.Permissions["contents"] = github.PermissionWrite
	}
	return token, nil
}

func (f *fakeRerunTokens) repositoryName() string {
	if f.repository != "" {
		return f.repository
	}
	return "owner/repo"
}

func TestRuntimeAcceptsCanonicalRepositoryResolvedFromInternalID(t *testing.T) {
	store := newGrantTestStore(t)
	grant := testGrant("grant-internal-repository")
	grant.RepositoryID = "internal-repository-row"
	ctx := context.Background()
	if err := store.ReplaceGrant(ctx, &grant); err != nil {
		t.Fatal(err)
	}
	lease, err := store.IssueLease(ctx, testLeaseClaim(grant))
	if err != nil {
		t.Fatal(err)
	}
	authority := &fakeLeaseAuthority{verified: VerifiedLease{
		LeaseID: lease.ID, GrantID: grant.ID, Expected: testMintClaim(grant, lease).Expected,
		AppRegistrationID: "app-registration-1", InstallationID: 42, CanonicalRepository: "owner/repo"}}
	tokens := &fakeRerunTokens{repository: "owner/repo"}
	runtime, err := NewRuntime(store, authority, tokens)
	if err != nil {
		t.Fatal(err)
	}
	token, err := runtime.Redeem(ctx, lease.ID)
	if err != nil || token.Token == "" {
		t.Fatalf("redeem internal repository ID via canonical provider repository: %+v, %v", token, err)
	}
}

func TestRuntimeStopRevokesHeldTokenAndPreservesFailedRevocation(t *testing.T) {
	runtime, store, _, tokens, _, lease := newRuntimeTestFixture(t)
	ctx := context.Background()
	if _, err := runtime.Redeem(ctx, lease.ID); err != nil {
		t.Fatal(err)
	}
	tokens.revokeErr = errors.New("provider unavailable")
	if err := runtime.Stop(ctx); !errors.Is(err, ErrRevocationUnconfirmed) {
		t.Fatalf("stop error = %v, want unconfirmed revocation", err)
	}
	if state, err := store.ExposureStateAt(ctx, lease.ID, time.Now()); err != nil ||
		state != ExposureResidual {
		t.Fatalf("stopped exposure = %s, err = %v", state, err)
	}
	audits, err := store.ListLeaseAudits(ctx, lease.ID)
	if err != nil {
		t.Fatal(err)
	}
	foundPending := false
	for _, audit := range audits {
		foundPending = foundPending || audit.Outcome == AuditRevocationPending
	}
	if !foundPending {
		t.Fatalf("missing failed-revocation receipt: %+v", audits)
	}
	if token, err := runtime.Redeem(ctx, lease.ID); !errors.Is(err, ErrGrantUnavailable) || token.Token != "" {
		t.Fatalf("redemption after stop = %+v, err = %v", token, err)
	}
}

func TestRuntimeStopWinsInflightMintBeforeExport(t *testing.T) {
	runtime, store, _, tokens, _, lease := newRuntimeTestFixture(t)
	started := make(chan struct{})
	release := make(chan struct{})
	tokens.onMint = func() { close(started); <-release }
	type result struct {
		token github.InstallationToken
		err   error
	}
	redemption := make(chan result, 1)
	go func() {
		token, err := runtime.Redeem(context.Background(), lease.ID)
		redemption <- result{token: token, err: err}
	}()
	<-started
	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	close(release)
	got := <-redemption
	if !errors.Is(got.err, ErrGrantUnavailable) || got.token.Token != "" || tokens.revocations != 1 {
		t.Fatalf("inflight redemption after stop = %+v, revocations = %d", got, tokens.revocations)
	}
	if state, err := store.ExposureStateAt(context.Background(), lease.ID, time.Now()); err != nil ||
		state != ExposureUnknown {
		t.Fatalf("stopped inflight exposure = %s, err = %v", state, err)
	}
}

func TestRuntimeDeniesMalformedCanonicalRepositoryBeforeMint(t *testing.T) {
	for _, repository := range []string{"", "repo-1", " owner/repo", "owner//repo"} {
		t.Run(repository, func(t *testing.T) {
			runtime, _, authority, tokens, _, lease := newRuntimeTestFixture(t)
			authority.verified.CanonicalRepository = repository
			if token, err := runtime.Redeem(context.Background(), lease.ID); !errors.Is(err, ErrGrantUnavailable) || token.Token != "" {
				t.Fatalf("malformed repository %q yielded token %+v, err %v", repository, token, err)
			}
			if tokens.mintCount != 0 {
				t.Fatalf("malformed repository %q minted %d tokens", repository, tokens.mintCount)
			}
		})
	}
}

func (f *fakeRerunTokens) Revoke(_ context.Context, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revocations++
	return f.revokeErr
}

func newRuntimeTestFixture(t *testing.T) (*Runtime, *Store, *fakeLeaseAuthority, *fakeRerunTokens, Grant, *Lease) {
	t.Helper()
	store := newGrantTestStore(t)
	grant := testGrant("grant-1")
	if err := store.ReplaceGrant(context.Background(), &grant); err != nil {
		t.Fatal(err)
	}
	lease, err := store.IssueLease(context.Background(), testLeaseClaim(grant))
	if err != nil {
		t.Fatal(err)
	}
	authority := &fakeLeaseAuthority{verified: VerifiedLease{
		LeaseID: lease.ID, GrantID: grant.ID,
		Expected:          testMintClaim(grant, lease).Expected,
		AppRegistrationID: "app-registration-1", InstallationID: 42, CanonicalRepository: "owner/repo"}}
	tokens := &fakeRerunTokens{}
	runtime, err := NewRuntime(store, authority, tokens)
	if err != nil {
		t.Fatal(err)
	}
	return runtime, store, authority, tokens, grant, lease
}

func TestRuntimeRedeemIsOneShotAndReleasesExactToken(t *testing.T) {
	runtime, store, _, tokens, _, lease := newRuntimeTestFixture(t)
	ctx := context.Background()
	issued, err := runtime.Redeem(ctx, lease.ID)
	if err != nil || issued.Token != "fake-secret" {
		t.Fatalf("redemption = %+v, err = %v", issued, err)
	}
	if _, err := runtime.Redeem(ctx, lease.ID); !errors.Is(err, ErrLeaseAlreadyRedeemed) {
		t.Fatalf("second redemption = %v", err)
	}
	if tokens.mintCount != 1 {
		t.Fatalf("mint count = %d", tokens.mintCount)
	}
	if err := runtime.Release(ctx, lease.ID); err != nil {
		t.Fatal(err)
	}
	if tokens.revocations != 1 {
		t.Fatalf("revocations = %d", tokens.revocations)
	}
	if state, err := store.ExposureStateAt(ctx, lease.ID, time.Now()); err != nil ||
		state != ExposureRevokedAtProvider {
		t.Fatalf("released exposure = %s, err = %v", state, err)
	}
}

func TestRuntimeAuditedRedemptionPersistsHashedRequestBeforeReturn(t *testing.T) {
	runtime, store, _, _, _, lease := newRuntimeTestFixture(t)
	ctx := context.Background()
	if _, err := runtime.RedeemWithAudit(ctx, lease.ID, "opaque-request-1"); err != nil {
		t.Fatal(err)
	}
	var auditID string
	if err := store.db.GetContext(ctx, &auditID, store.db.Rebind(`SELECT id
  FROM provider_access_audit WHERE lease_id = ? AND outcome = ?`),
		lease.ID, AuditTokenIssued); err != nil {
		t.Fatal(err)
	}
	audit, err := store.GetAudit(ctx, auditID)
	if err != nil || audit == nil || audit.RequestIDHash == "" ||
		audit.RequestIDHash == "opaque-request-1" {
		t.Fatalf("audited redemption = %+v, err = %v", audit, err)
	}
}

func TestRuntimeRevokesStoppedSessionWithoutAffectingOtherSession(t *testing.T) {
	runtime, store, _, tokens, _, lease := newRuntimeTestFixture(t)
	ctx := context.Background()
	if _, err := runtime.Redeem(ctx, lease.ID); err != nil {
		t.Fatal(err)
	}
	if err := runtime.RevokeSession(ctx, "session-other"); err != nil {
		t.Fatal(err)
	}
	if tokens.revocations != 0 {
		t.Fatalf("unrelated session revocations = %d", tokens.revocations)
	}
	if err := runtime.RevokeSession(ctx, lease.SessionID); err != nil {
		t.Fatal(err)
	}
	if tokens.revocations != 1 {
		t.Fatalf("stopped session revocations = %d", tokens.revocations)
	}
	active, err := store.GetActiveLease(ctx, lease.ID)
	if err != nil || active != nil {
		t.Fatalf("stopped session active lease = %+v, err = %v", active, err)
	}
	state, err := store.ExposureStateAt(ctx, lease.ID, time.Now())
	if err != nil || state != ExposureRevokedAtProvider {
		t.Fatalf("stopped session exposure = %s, err = %v", state, err)
	}
}

func TestRuntimeDeniesDriftAndRevokesUnexportedToken(t *testing.T) {
	runtime, store, authority, tokens, _, lease := newRuntimeTestFixture(t)
	authority.drift = true
	if token, err := runtime.Redeem(context.Background(), lease.ID); !errors.Is(err, ErrGrantUnavailable) ||
		token.Token != "" || tokens.revocations != 1 {
		t.Fatalf("drift redemption = %+v, err = %v, revocations = %d", token, err, tokens.revocations)
	}
	if state, err := store.ExposureStateAt(context.Background(), lease.ID, time.Now()); err != nil ||
		state != ExposureUnknown {
		t.Fatalf("drift exposure = %s, err = %v", state, err)
	}
}

func TestRuntimeRejectsOverbroadTokenFromInjectedSource(t *testing.T) {
	runtime, store, _, tokens, _, lease := newRuntimeTestFixture(t)
	tokens.overbroad = true
	if token, err := runtime.Redeem(context.Background(), lease.ID); !errors.Is(err, ErrProviderTokenScope) ||
		token.Token != "" || tokens.revocations != 1 {
		t.Fatalf("overbroad runtime token = %+v, err = %v, revocations = %d",
			token, err, tokens.revocations)
	}
	if state, err := store.ExposureStateAt(context.Background(), lease.ID, time.Now()); err != nil ||
		state != ExposureUnknown {
		t.Fatalf("overbroad exposure = %s, err = %v", state, err)
	}
}

func TestRuntimeWorkspaceFenceRecordsFailedRevocationResidual(t *testing.T) {
	runtime, store, _, tokens, grant, lease := newRuntimeTestFixture(t)
	ctx := context.Background()
	if _, err := runtime.Redeem(ctx, lease.ID); err != nil {
		t.Fatal(err)
	}
	tokens.revokeErr = errors.New("provider echoed fake-secret")
	result, err := runtime.FenceWorkspace(ctx, grant.WorkspaceID)
	if !errors.Is(err, ErrRevocationUnconfirmed) ||
		len(result.ExposedLeaseIDs) != 1 || result.ExposedLeaseIDs[0] != lease.ID {
		t.Fatalf("failed fence revoke = %+v, err = %v", result, err)
	}
	if state, err := store.ExposureStateAt(ctx, lease.ID, time.Now()); err != nil ||
		state != ExposureResidual {
		t.Fatalf("residual exposure = %s, err = %v", state, err)
	}
	if _, err := runtime.Redeem(ctx, lease.ID); !errors.Is(err, ErrGrantUnavailable) {
		t.Fatalf("fenced redemption = %v", err)
	}
}
