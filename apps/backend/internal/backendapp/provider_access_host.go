package backendapp

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/kandev/kandev/internal/provideraccess"
	"github.com/kandev/kandev/pkg/pluginsdk"
)

// providerHostAccess owns one credential runtime per connected plugin identity.
// Production must install it only after all lifecycle fences are connected.
type providerHostAccess struct {
	store    *provideraccess.Store
	grants   *providerGrantAuthority
	managed  leaseManagedReader
	provider leaseProviderReader
	tokens   provideraccess.RerunTokenSource
	mu       sync.Mutex
	runtimes map[string]*provideraccess.Runtime
	blocked  map[string]bool
	stopped  bool
}

func (s *providerHostAccess) authority(pluginID string) *providerLeaseAuthority {
	return &providerLeaseAuthority{store: s.store, grants: s.grants,
		managed: s.managed, provider: s.provider, pluginID: pluginID}
}

func (s *providerHostAccess) runtime(pluginID string) (*provideraccess.Runtime, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped || s.blocked[pluginID] || pluginID == "" || s.tokens == nil {
		return nil, provideraccess.ErrGrantUnavailable
	}
	if runtime := s.runtimes[pluginID]; runtime != nil {
		return runtime, nil
	}
	runtime, err := provideraccess.NewRuntime(s.store, s.authority(pluginID), s.tokens)
	if err != nil {
		return nil, err
	}
	if s.runtimes == nil {
		s.runtimes = make(map[string]*provideraccess.Runtime)
	}
	s.runtimes[pluginID] = runtime
	return runtime, nil
}

func (s *providerHostAccess) Issue(ctx context.Context, pluginID string,
	spec pluginsdk.ProviderAccessLeaseSpec) (pluginsdk.ProviderAccessLease, error) {
	if _, err := s.runtime(pluginID); err != nil {
		return pluginsdk.ProviderAccessLease{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return pluginsdk.ProviderAccessLease{}, provideraccess.ErrGrantUnavailable
	}
	grant, err := s.store.GetGrant(ctx, spec.GrantID)
	if err != nil || grant == nil || !requestMatchesGrant(pluginID, spec, grant) {
		return pluginsdk.ProviderAccessLease{}, provideraccess.ErrGrantUnavailable
	}
	target := providerTargetFromSDK(spec.Target)
	lease, err := s.authority(pluginID).Issue(ctx, spec.GrantID, spec.ManagedTaskID,
		spec.SessionID, target, spec.IdempotencyKey)
	if err != nil {
		return pluginsdk.ProviderAccessLease{}, err
	}
	verified, err := s.authority(pluginID).VerifyLease(ctx, lease.ID)
	if err != nil {
		return pluginsdk.ProviderAccessLease{}, provideraccess.ErrGrantUnavailable
	}
	return pluginsdk.ProviderAccessLease{LeaseID: lease.ID, ExpiresAt: lease.ExpiresAt,
		TargetDigest: lease.TargetDigest, GrantGeneration: lease.GrantGeneration,
		ApprovalRevision: lease.ApprovalRevision, ConnectionGeneration: lease.ConnectionGeneration,
		CanonicalRepository: verified.CanonicalRepository, Target: spec.Target}, nil
}

func requestMatchesGrant(pluginID string, spec pluginsdk.ProviderAccessLeaseSpec,
	grant *provideraccess.Grant) bool {
	return pluginID != "" && spec.RequestID != "" && spec.IdempotencyKey != "" &&
		grant.PluginID == pluginID && grant.WorkspaceID == spec.WorkspaceID &&
		grant.TargetTaskID == spec.TargetTaskID && grant.RepositoryID == spec.RepositoryID &&
		grant.Provider == spec.Provider && grant.Purpose == spec.Purpose &&
		spec.ManagedTaskID != "" && spec.SessionID != ""
}

func providerTargetFromSDK(target pluginsdk.ProviderAccessGitHubRerunTarget) provideraccess.GitHubRerunTarget {
	return provideraccess.GitHubRerunTarget{Operation: target.Operation, PRNumber: int(target.PRNumber),
		BaseRepositoryID: target.BaseRepositoryID, BaseRepository: target.BaseRepository,
		BaseRef: target.BaseRef, BaseSHA: target.BaseSHA,
		HeadRepositoryID: target.HeadRepositoryID, HeadRepository: target.HeadRepository,
		HeadRef: target.HeadRef, HeadSHA: target.HeadSHA, SourceRunID: target.SourceRunID,
		SourceAttempt: int(target.SourceAttempt), WorkflowID: target.WorkflowID}
}

func (s *providerHostAccess) Redeem(ctx context.Context, pluginID, requestID,
	leaseID string) (pluginsdk.ProviderAccessCredential, error) {
	if requestID == "" || !s.ownsLease(ctx, pluginID, leaseID) {
		return pluginsdk.ProviderAccessCredential{}, provideraccess.ErrGrantUnavailable
	}
	runtime, err := s.runtime(pluginID)
	if err != nil {
		return pluginsdk.ProviderAccessCredential{}, err
	}
	token, err := runtime.Redeem(ctx, leaseID)
	if err != nil {
		return pluginsdk.ProviderAccessCredential{}, err
	}
	return pluginsdk.NewProviderAccessCredential(token.Token, token.ExpiresAt,
		token.Principal.PrincipalID, token.Repositories[0].FullName, "github_actions_rerun"), nil
}

func (s *providerHostAccess) Release(ctx context.Context, pluginID, requestID,
	leaseID string) (bool, error) {
	if requestID == "" || !s.ownsLease(ctx, pluginID, leaseID) {
		return false, provideraccess.ErrGrantUnavailable
	}
	runtime, err := s.runtime(pluginID)
	if err != nil {
		return false, err
	}
	if err := runtime.Release(ctx, leaseID); err != nil {
		return false, err
	}
	return true, nil
}

func (s *providerHostAccess) ownsLease(ctx context.Context, pluginID, leaseID string) bool {
	if s == nil || s.store == nil || pluginID == "" {
		return false
	}
	owner, err := s.store.GetLeasePluginID(ctx, leaseID)
	return err == nil && owner == pluginID
}

func (s *providerHostAccess) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopped = true
	var result error
	for _, runtime := range s.runtimes {
		result = errors.Join(result, runtime.Stop(ctx))
	}
	return result
}

func (s *providerHostAccess) GetGrant(ctx context.Context, id string) (*provideraccess.Grant, error) {
	return s.store.GetGrant(ctx, id)
}

func (s *providerHostAccess) ListWorkspaceGrants(ctx context.Context, workspaceID string) ([]provideraccess.Grant, error) {
	return s.store.ListWorkspaceGrants(ctx, workspaceID)
}

// RevokeGrant fences the persisted grant before attempting exact-token
// revocation. A failed provider response stays visible as residual exposure.
func (s *providerHostAccess) RevokeGrant(ctx context.Context, workspaceID,
	grantID string, at time.Time) error {
	if at.IsZero() {
		return provideraccess.ErrGrantUnavailable
	}
	grant, err := s.store.GetGrant(ctx, grantID)
	if err != nil {
		return err
	}
	if grant == nil || grant.WorkspaceID != workspaceID {
		return provideraccess.ErrGrantUnavailable
	}
	runtime, err := s.runtime(grant.PluginID)
	if err != nil {
		return err
	}
	return runtime.RevokeGrant(ctx, workspaceID, grantID)
}

// ReplaceGrant revokes the previous exact-scope bearer before admitting a
// successor generation. A failed revocation leaves the scope fenced.
func (s *providerHostAccess) ReplaceGrant(ctx context.Context, grant *provideraccess.Grant) error {
	if grant == nil {
		return provideraccess.ErrGrantUnavailable
	}
	if _, err := s.runtime(grant.PluginID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return provideraccess.ErrGrantUnavailable
	}
	active, err := s.store.GetActiveGrant(ctx, grant.Scope())
	if err != nil {
		return err
	}
	if active != nil {
		if err := s.runtimes[active.PluginID].RevokeGrant(ctx, active.WorkspaceID, active.ID); err != nil {
			return err
		}
	}
	return s.store.ReplaceGrant(ctx, grant)
}

// CleanupWorkspaceProviderAccess fences durable admission before attempting
// every in-memory exact-token revocation held by this Host.
func (s *providerHostAccess) CleanupWorkspaceProviderAccess(ctx context.Context, workspaceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.runtimes) == 0 {
		_, err := s.store.FenceWorkspace(ctx, workspaceID)
		return err
	}
	var result error
	for _, runtime := range s.runtimes {
		_, err := runtime.FenceWorkspace(ctx, workspaceID)
		result = errors.Join(result, err)
	}
	return result
}

// StopPlugin closes this plugin's transient runtime and fences every retained
// grant. A failed provider revocation leaves its runtime available for retry.
func (s *providerHostAccess) StopPlugin(ctx context.Context, pluginID string) error {
	if pluginID == "" {
		return provideraccess.ErrGrantUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var result error
	if runtime := s.runtimes[pluginID]; runtime != nil {
		result = errors.Join(result, runtime.Stop(ctx))
	}
	grants, err := s.store.ListPluginGrants(ctx, pluginID)
	if err != nil {
		if s.blocked == nil {
			s.blocked = make(map[string]bool)
		}
		s.blocked[pluginID] = true
		return errors.Join(result, err)
	}
	for _, grant := range grants {
		if grant.RevokedAt == nil {
			result = errors.Join(result, s.store.RevokeGrant(ctx,
				grant.WorkspaceID, grant.ID, time.Now().UTC()))
		}
	}
	if result == nil {
		delete(s.runtimes, pluginID)
		delete(s.blocked, pluginID)
	} else {
		if s.blocked == nil {
			s.blocked = make(map[string]bool)
		}
		s.blocked[pluginID] = true
	}
	return result
}
