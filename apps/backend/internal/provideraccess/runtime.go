package provideraccess

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kandev/kandev/internal/github"
)

// VerifiedLease is resolved from current Host records for this plugin
// connection. Its fields cannot be copied from a plugin request.
type VerifiedLease struct {
	LeaseID             string
	GrantID             string
	Expected            FinalLeaseIdentity
	AppRegistrationID   string
	InstallationID      int64
	CanonicalRepository string
}

// LeaseAuthority verifies the connected plugin, current managed session,
// approval, linked target, and provider connection on every call.
type LeaseAuthority interface {
	VerifyLease(context.Context, string) (VerifiedLease, error)
}

type RerunTokenSource interface {
	Mint(context.Context, string, int64, string) (github.InstallationToken, error)
	Revoke(context.Context, string) error
}

type activeToken struct {
	grantID     string
	workspaceID string
	sessionID   string
	value       string
	expiresAt   time.Time
}

// Runtime owns only transient exact-token revocation material. Its ledger
// remains authoritative after process loss; a restart never recreates tokens.
type Runtime struct {
	store           *Store
	authority       LeaseAuthority
	tokens          RerunTokenSource
	mu              sync.Mutex
	active          map[string]activeToken
	blockedSessions map[string]bool
	stopped         bool
}

func NewRuntime(store *Store, authority LeaseAuthority, tokens RerunTokenSource) (*Runtime, error) {
	if store == nil || authority == nil || tokens == nil {
		return nil, errors.New("provider access runtime dependencies are required")
	}
	return &Runtime{store: store, authority: authority, tokens: tokens,
		active: make(map[string]activeToken), blockedSessions: make(map[string]bool)}, nil
}

// Redeem returns one token only after a durable one-shot claim, fresh
// authority recheck, and serialized final exposure admission.
func (r *Runtime) Redeem(ctx context.Context, leaseID string) (github.InstallationToken, error) {
	return r.redeem(ctx, leaseID, "")
}

// RedeemWithAudit persists a hashed RPC request correlation atomically with
// exposure admission. The raw request ID never enters the ledger or logs.
func (r *Runtime) RedeemWithAudit(ctx context.Context, leaseID, requestID string) (github.InstallationToken, error) {
	requestHash, err := HashRequestID(requestID)
	if err != nil {
		return github.InstallationToken{}, ErrGrantUnavailable
	}
	return r.redeem(ctx, leaseID, requestHash)
}

func (r *Runtime) redeem(ctx context.Context, leaseID, requestHash string) (github.InstallationToken, error) {
	r.mu.Lock()
	stopped := r.stopped
	r.mu.Unlock()
	if stopped {
		return github.InstallationToken{}, ErrGrantUnavailable
	}
	first, err := r.authority.VerifyLease(ctx, leaseID)
	if err != nil {
		return github.InstallationToken{}, ErrGrantUnavailable
	}
	if !validVerifiedLease(first, leaseID) {
		return github.InstallationToken{}, ErrGrantUnavailable
	}
	r.mu.Lock()
	blocked := r.blockedSessions[first.Expected.SessionID]
	r.mu.Unlock()
	if blocked {
		return github.InstallationToken{}, ErrGrantUnavailable
	}
	if _, err := r.store.ClaimMintIntent(ctx, MintClaim{
		LeaseID: leaseID, GrantID: first.GrantID, Expected: first.Expected,
	}); err != nil {
		return github.InstallationToken{}, err
	}
	token, err := r.tokens.Mint(ctx, first.AppRegistrationID, first.InstallationID, first.CanonicalRepository)
	if err != nil {
		return github.InstallationToken{}, err
	}
	permissions := github.InstallationPermissions{
		"actions": github.PermissionWrite, "pull_requests": github.PermissionRead,
		"metadata": github.PermissionRead,
	}
	if !validRerunToken(token, first.InstallationID, first.CanonicalRepository, permissions) {
		return github.InstallationToken{}, r.revokeUnexported(ctx, token.Token, ErrProviderTokenScope)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped || r.blockedSessions[first.Expected.SessionID] {
		return github.InstallationToken{}, r.revokeUnexported(ctx, token.Token, ErrGrantUnavailable)
	}
	current, verifyErr := r.authority.VerifyLease(ctx, leaseID)
	if verifyErr != nil || current != first {
		return github.InstallationToken{}, r.revokeUnexported(ctx, token.Token, ErrGrantUnavailable)
	}
	receipt := ExposureReceipt{
		LeaseID: leaseID, GrantID: first.GrantID, Provider: "github",
		ProviderPrincipalID: token.Principal.PrincipalID,
		RepositoryID:        first.Expected.Scope.RepositoryID,
		PermissionProfile:   "github_actions_rerun",
		ProviderExpiresAt:   token.ExpiresAt, Expected: first.Expected,
	}
	revoke := func(revokeCtx context.Context) error { return r.tokens.Revoke(revokeCtx, token.Token) }
	var exposureErr error
	if requestHash == "" {
		exposureErr = r.store.RecordExposureOrRevoke(ctx, receipt, revoke)
	} else {
		exposureErr = r.store.RecordExposureWithAuditOrRevoke(ctx, receipt,
			redemptionAudit(first, leaseID, requestHash), revoke)
	}
	if exposureErr != nil {
		return github.InstallationToken{}, exposureErr
	}
	r.active[leaseID] = activeToken{grantID: first.GrantID,
		workspaceID: first.Expected.Scope.WorkspaceID,
		sessionID:   first.Expected.SessionID,
		value:       token.Token, expiresAt: token.ExpiresAt}
	return token, nil
}

func redemptionAudit(verified VerifiedLease, leaseID, requestHash string) AuditEvent {
	expected := verified.Expected
	return AuditEvent{ID: uuid.NewString(), GrantID: verified.GrantID, LeaseID: leaseID,
		PluginInstallationID: expected.Scope.PluginInstallationID,
		WorkspaceID:          expected.Scope.WorkspaceID, ManagedTaskID: expected.ManagedTaskID,
		SessionID: expected.SessionID, TargetDigest: expected.TargetDigest,
		GrantGeneration: expected.GrantGeneration, ApprovalRevision: expected.ApprovalRevision,
		ConnectionGeneration: expected.ConnectionGeneration, Provider: expected.Scope.Provider,
		Purpose: expected.Scope.Purpose, Outcome: AuditTokenIssued,
		RequestIDHash: requestHash, At: time.Now().UTC()}
}

// RevokeSession fences durable leases and revokes any exact token already
// exported for this managed session. Failed provider revocation remains in the
// exposure ledger and the session stays blocked in this runtime.
func (r *Runtime) RevokeSession(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return ErrGrantUnavailable
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.blockedSessions[sessionID] = true
	result := r.store.RevokeSessionLeases(ctx, sessionID)
	for leaseID, token := range r.active {
		if token.sessionID == sessionID {
			result = errors.Join(result, r.revokeActive(ctx, leaseID, token))
		}
	}
	return result
}

// RevokeWorkspaceTokens retries every exact token still held for a workspace
// after its grants have been fenced. It does not permanently delete workspace
// grant state, so a new connection can receive a new grant after confirmation.
func (r *Runtime) RevokeWorkspaceTokens(ctx context.Context, workspaceID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result error
	for leaseID, token := range r.active {
		if token.workspaceID == workspaceID {
			result = errors.Join(result, r.revokeActive(ctx, leaseID, token))
		}
	}
	return result
}

// Stop closes redemption and attempts to revoke every exact token still held
// in memory. Failed provider revocation remains visible in the durable ledger.
func (r *Runtime) Stop(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopped = true
	var result error
	for leaseID, token := range r.active {
		result = errors.Join(result, r.revokeActive(ctx, leaseID, token))
	}
	return result
}

func validVerifiedLease(lease VerifiedLease, requestedID string) bool {
	return lease.LeaseID == requestedID && lease.GrantID != "" &&
		lease.AppRegistrationID != "" && lease.InstallationID > 0 && lease.CanonicalRepository != "" &&
		lease.Expected.Scope.RepositoryID != "" && validCanonicalGitHubRepository(lease.CanonicalRepository) &&
		lease.Expected.Scope.Provider == "github" &&
		lease.Expected.Scope.Purpose == "actions_write"
}

func (r *Runtime) revokeUnexported(ctx context.Context, token string, cause error) error {
	revokeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if r.tokens.Revoke(revokeCtx, token) != nil {
		return errors.Join(cause, ErrRevocationUnconfirmed)
	}
	return cause
}

// RevokeGrant fences the ledger and revokes every still-held token for that
// grant. Provider failure remains visible in the exposure receipt.
func (r *Runtime) RevokeGrant(ctx context.Context, workspaceID, grantID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.store.RevokeGrant(ctx, workspaceID, grantID, time.Now().UTC()); err != nil {
		return err
	}
	var result error
	for leaseID, token := range r.active {
		if token.grantID == grantID && token.workspaceID == workspaceID {
			result = errors.Join(result, r.revokeActive(ctx, leaseID, token))
		}
	}
	return result
}

// Release revokes the exact exported token and records the provider outcome.
func (r *Runtime) Release(ctx context.Context, leaseID string) error {
	return r.release(ctx, leaseID, "")
}

// ReleaseWithAudit hashes the RPC request identity before revoking the exact
// exported token. Its outcome receipt is durable even on provider failure.
func (r *Runtime) ReleaseWithAudit(ctx context.Context, leaseID, requestID string) error {
	requestHash, err := HashRequestID(requestID)
	if err != nil {
		return ErrGrantUnavailable
	}
	return r.release(ctx, leaseID, requestHash)
}

func (r *Runtime) release(ctx context.Context, leaseID, requestHash string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	token, ok := r.active[leaseID]
	if !ok {
		return ErrRevocationUnconfirmed
	}
	return r.revokeActiveWithAudit(ctx, leaseID, token, requestHash)
}

func (r *Runtime) revokeActive(ctx context.Context, leaseID string, token activeToken) error {
	return r.revokeActiveWithAudit(ctx, leaseID, token, "")
}

func (r *Runtime) revokeActiveWithAudit(ctx context.Context, leaseID string,
	token activeToken, requestHash string) error {
	revokeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	confirmed := r.tokens.Revoke(revokeCtx, token.value) == nil
	var err error
	if requestHash == "" {
		err = r.store.RecordRevocationResult(revokeCtx, leaseID, time.Now().UTC(), confirmed)
	} else {
		err = r.store.RecordRevocationResultWithAudit(revokeCtx, leaseID,
			time.Now().UTC(), confirmed, requestHash)
	}
	if err != nil {
		return err
	}
	if !confirmed {
		return ErrRevocationUnconfirmed
	}
	delete(r.active, leaseID)
	return nil
}

// FenceWorkspace preserves unexpired residual receipts and revokes every
// exact token still held by this Host. Missing in-memory tokens are reported
// by the returned IDs; they cannot be reconstructed after a restart.
func (r *Runtime) FenceWorkspace(ctx context.Context, workspaceID string) (WorkspaceFenceResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	result, err := r.store.FenceWorkspace(ctx, workspaceID)
	if err != nil {
		return WorkspaceFenceResult{}, err
	}
	for _, leaseID := range result.ExposedLeaseIDs {
		if token, ok := r.active[leaseID]; ok {
			err = errors.Join(err, r.revokeActive(ctx, leaseID, token))
		}
	}
	return result, err
}

// CleanupWorkspaceProviderAccess is the task workspace-delete lifecycle seam.
func (r *Runtime) CleanupWorkspaceProviderAccess(ctx context.Context, workspaceID string) error {
	_, err := r.FenceWorkspace(ctx, workspaceID)
	return err
}
