package backendapp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kandev/kandev/internal/github"
	"github.com/kandev/kandev/internal/plugins"
	"github.com/kandev/kandev/internal/provideraccess"
)

type leaseManagedReader interface {
	VerifyManagedSession(context.Context, string, string, string, string, string) (bool, error)
}

const providerPRStateOpen = "open"

type leaseProviderReader interface {
	GetTaskPRByOwnerRepoNumber(context.Context, string, string, string, int) (*github.TaskPR, error)
	GetPRForAutomation(context.Context, string, string, string, int) (*github.PR, error)
	ListWorkflowRunsForAutomation(context.Context, string, string, string, string) ([]github.WorkflowRun, error)
}

// providerLeaseAuthority is bound to one plugin connection, not to a task
// caller's asserted plugin identity. Production credential export remains off
// until the connection-bound RPC and lifecycle hooks are composed.
type providerLeaseAuthority struct {
	store    *provideraccess.Store
	grants   *providerGrantAuthority
	managed  leaseManagedReader
	provider leaseProviderReader
	pluginID string
}

var _ provideraccess.LeaseAuthority = (*providerLeaseAuthority)(nil)

func (a *providerLeaseAuthority) Issue(
	ctx context.Context, grantID, managedTaskID, sessionID string,
	target provideraccess.GitHubRerunTarget, idempotencyKey string,
) (*provideraccess.Lease, error) {
	verified, err := a.verifyCurrent(ctx, grantID, managedTaskID, sessionID, target)
	if err != nil {
		return nil, err
	}
	claim := provideraccess.LeaseClaim{
		ID: uuid.NewString(), GrantID: grantID,
		GrantGeneration: verified.Expected.GrantGeneration, Scope: verified.Expected.Scope,
		ManagedTaskID: managedTaskID, SessionID: sessionID,
		TargetDigest: verified.Expected.TargetDigest, Target: &target,
		ApprovalRevision:     verified.Expected.ApprovalRevision,
		ConnectionGeneration: verified.Expected.ConnectionGeneration,
		IdempotencyKey:       idempotencyKey, ExpiresAt: time.Now().UTC().Add(2 * time.Minute),
	}
	return a.store.IssueLease(ctx, claim)
}

func (a *providerLeaseAuthority) VerifyLease(ctx context.Context, leaseID string) (provideraccess.VerifiedLease, error) {
	if a == nil || a.store == nil || leaseID == "" {
		return provideraccess.VerifiedLease{}, provideraccess.ErrGrantUnavailable
	}
	lease, err := a.store.GetActiveLease(ctx, leaseID)
	if err != nil || lease == nil {
		return provideraccess.VerifiedLease{}, provideraccess.ErrGrantUnavailable
	}
	target, err := a.store.GetLeaseTarget(ctx, leaseID)
	if err != nil || target == nil {
		return provideraccess.VerifiedLease{}, provideraccess.ErrGrantUnavailable
	}
	verified, err := a.verifyCurrent(ctx, lease.GrantID, lease.ManagedTaskID, lease.SessionID, *target)
	if err != nil || !leaseMatchesVerified(lease, verified) {
		return provideraccess.VerifiedLease{}, provideraccess.ErrGrantUnavailable
	}
	verified.LeaseID = leaseID
	return verified, nil
}

func leaseMatchesVerified(lease *provideraccess.Lease, verified provideraccess.VerifiedLease) bool {
	return lease != nil && lease.GrantID == verified.GrantID &&
		lease.GrantGeneration == verified.Expected.GrantGeneration &&
		lease.Scope == verified.Expected.Scope && lease.ManagedTaskID == verified.Expected.ManagedTaskID &&
		lease.SessionID == verified.Expected.SessionID && lease.TargetDigest == verified.Expected.TargetDigest &&
		lease.ApprovalRevision == verified.Expected.ApprovalRevision &&
		lease.ConnectionGeneration == verified.Expected.ConnectionGeneration
}

func (a *providerLeaseAuthority) verifyCurrent(
	ctx context.Context, grantID, managedTaskID, sessionID string, target provideraccess.GitHubRerunTarget,
) (provideraccess.VerifiedLease, error) {
	if !a.ready() || grantID == "" || managedTaskID == "" || sessionID == "" {
		return provideraccess.VerifiedLease{}, provideraccess.ErrGrantUnavailable
	}
	grant, err := a.currentGrant(ctx, grantID)
	if err != nil {
		return provideraccess.VerifiedLease{}, err
	}
	live, err := a.managed.VerifyManagedSession(ctx, a.pluginID, grant.WorkspaceID,
		grant.ConversationKey, managedTaskID, sessionID)
	if err != nil || !live {
		return provideraccess.VerifiedLease{}, provideraccess.ErrGrantUnavailable
	}
	canonical, err := a.verifyTargetEvidence(ctx, grant, target)
	if err != nil {
		return provideraccess.VerifiedLease{}, err
	}
	approval, connection, err := a.currentApprovalAndConnection(ctx, grant)
	if err != nil {
		return provideraccess.VerifiedLease{}, err
	}
	digest, err := target.Digest()
	if err != nil {
		return provideraccess.VerifiedLease{}, provideraccess.ErrGrantUnavailable
	}
	return provideraccess.VerifiedLease{GrantID: grant.ID,
		Expected: provideraccess.FinalLeaseIdentity{
			GrantGeneration: grant.Generation, Scope: grant.Scope(),
			ManagedTaskID: managedTaskID, SessionID: sessionID, TargetDigest: digest,
			ApprovalRevision: approval.Revision,
			ConnectionGeneration: plugins.CanonicalApprovalDigest(connection.AppRegistrationID,
				fmt.Sprint(*connection.InstallationID), fmt.Sprint(connection.CredentialGeneration)),
		}, InstallationID: *connection.InstallationID, CanonicalRepository: canonical}, nil
}

func (a *providerLeaseAuthority) ready() bool {
	return a != nil && a.store != nil && a.grants != nil && a.managed != nil &&
		a.provider != nil && a.pluginID != ""
}

func (a *providerLeaseAuthority) currentGrant(ctx context.Context, grantID string) (*provideraccess.Grant, error) {
	grant, err := a.store.GetGrant(ctx, grantID)
	if err != nil || grant == nil || grant.PluginID != a.pluginID {
		return nil, provideraccess.ErrGrantUnavailable
	}
	active, err := a.store.GetActiveGrant(ctx, grant.Scope())
	if err != nil || active == nil || active.ID != grant.ID || active.Generation != grant.Generation {
		return nil, provideraccess.ErrGrantUnavailable
	}
	scope, err := a.grants.ResolveGrantScope(ctx, grant.Scope())
	if err != nil || scope != grant.Scope() {
		return nil, provideraccess.ErrGrantUnavailable
	}
	return grant, nil
}

func (a *providerLeaseAuthority) verifyTargetEvidence(
	ctx context.Context, grant *provideraccess.Grant, target provideraccess.GitHubRerunTarget,
) (string, error) {
	repo, err := a.grants.tasks.GetRepository(ctx, grant.RepositoryID)
	if err != nil || !validGrantRepository(repo, grant.WorkspaceID) {
		return "", provideraccess.ErrGrantUnavailable
	}
	canonical := repo.ProviderOwner + "/" + repo.ProviderName
	if !strings.EqualFold(canonical, target.BaseRepository) {
		return "", provideraccess.ErrGrantUnavailable
	}
	link, err := a.provider.GetTaskPRByOwnerRepoNumber(ctx, grant.TargetTaskID,
		repo.ProviderOwner, repo.ProviderName, target.PRNumber)
	if err != nil || !validLinkedProviderPR(link, grant, target) {
		return "", provideraccess.ErrGrantUnavailable
	}
	pr, err := a.provider.GetPRForAutomation(ctx, grant.WorkspaceID,
		repo.ProviderOwner, repo.ProviderName, target.PRNumber)
	if err != nil || pr == nil {
		return "", provideraccess.ErrGrantUnavailable
	}
	runs, err := a.provider.ListWorkflowRunsForAutomation(ctx, grant.WorkspaceID,
		repo.ProviderOwner, repo.ProviderName, target.HeadSHA)
	if err != nil {
		return "", provideraccess.ErrGrantUnavailable
	}
	for i := range runs {
		if runs[i].ID == target.SourceRunID &&
			target.MatchProviderEvidence(pr, &runs[i]) == nil {
			return canonical, nil
		}
	}
	return "", provideraccess.ErrGrantUnavailable
}

func validLinkedProviderPR(
	link *github.TaskPR, grant *provideraccess.Grant, target provideraccess.GitHubRerunTarget,
) bool {
	return link != nil && link.WorkspaceID == grant.WorkspaceID &&
		link.TaskID == grant.TargetTaskID && link.RepositoryID == grant.RepositoryID &&
		link.State == providerPRStateOpen && link.HeadSHA == target.HeadSHA
}

func (a *providerLeaseAuthority) currentApprovalAndConnection(
	ctx context.Context, grant *provideraccess.Grant,
) (plugins.CapabilityApprovalDTO, *github.WorkspaceConnection, error) {
	approval, ok, err := a.grants.plugins.GetCapabilityApproval(grant.PluginInstallationID, grant.WorkspaceID)
	if err != nil || !ok || !validGrantApproval(approval, grant.PluginInstallationID, grant.WorkspaceID) {
		return plugins.CapabilityApprovalDTO{}, nil, provideraccess.ErrGrantUnavailable
	}
	connection, err := a.grants.connections.GetWorkspaceConnection(ctx, grant.WorkspaceID)
	if err != nil || !validGrantAppConnection(connection, grant.WorkspaceID) ||
		connection.CredentialGeneration <= 0 {
		return plugins.CapabilityApprovalDTO{}, nil, provideraccess.ErrGrantUnavailable
	}
	return approval, connection, nil
}
