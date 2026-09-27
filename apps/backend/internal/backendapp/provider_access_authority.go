package backendapp

import (
	"context"
	"strings"

	"github.com/kandev/kandev/internal/authz"
	"github.com/kandev/kandev/internal/github"
	"github.com/kandev/kandev/internal/plugins"
	pluginstore "github.com/kandev/kandev/internal/plugins/store"
	"github.com/kandev/kandev/internal/provideraccess"
	"github.com/kandev/kandev/internal/task/models"
)

type grantTaskReader interface {
	AuthorizeWorkspaceScope(context.Context, string, authz.Scope) error
	GetTask(context.Context, string) (*models.Task, error)
	GetRepository(context.Context, string) (*models.Repository, error)
}

func (a *providerGrantAuthority) AuthorizeWorkspaceScope(ctx context.Context, workspaceID string, scope authz.Scope) error {
	if a == nil || a.tasks == nil || scope != authz.ScopeSecretManage {
		return provideraccess.ErrGrantUnavailable
	}
	return a.tasks.AuthorizeWorkspaceScope(ctx, workspaceID, scope)
}

var _ provideraccess.AdminGrantAuthority = (*providerGrantAuthority)(nil)

type grantPluginReader interface {
	Get(string) (*pluginstore.Record, error)
	GetCapabilityApproval(string, string) (plugins.CapabilityApprovalDTO, bool, error)
	AuthorizeCapability(string, string, string, uint64, string, string) plugins.ApprovalDecision
}

type grantConnectionReader interface {
	GetWorkspaceConnection(context.Context, string) (*github.WorkspaceConnection, error)
}

// providerGrantAuthority resolves a requested administrator grant against
// current Host records. The installation and provider principal are never
// selected by the request body.
type providerGrantAuthority struct {
	tasks       grantTaskReader
	plugins     grantPluginReader
	connections grantConnectionReader
}

func (a *providerGrantAuthority) ResolveGrantScope(
	ctx context.Context, requested provideraccess.GrantScope,
) (provideraccess.GrantScope, error) {
	if a == nil || a.tasks == nil || a.plugins == nil || a.connections == nil ||
		!validRequestedGrantScope(requested) {
		return provideraccess.GrantScope{}, provideraccess.ErrGrantUnavailable
	}
	installationID, err := a.resolveGrantInstallation(requested)
	if err != nil {
		return provideraccess.GrantScope{}, err
	}
	if err := a.verifyGrantTarget(ctx, requested); err != nil {
		return provideraccess.GrantScope{}, err
	}
	requested.PluginInstallationID = installationID
	return requested, nil
}

func (a *providerGrantAuthority) resolveGrantInstallation(requested provideraccess.GrantScope) (string, error) {
	record, err := a.plugins.Get(requested.PluginID)
	if err != nil || !validGrantPluginRecord(record, requested) {
		return "", provideraccess.ErrGrantUnavailable
	}
	approval, found, err := a.plugins.GetCapabilityApproval(record.InstallationID, requested.WorkspaceID)
	if err != nil || !found || !validGrantApproval(approval, record.InstallationID, requested.WorkspaceID) {
		return "", provideraccess.ErrGrantUnavailable
	}
	requestDigest := plugins.CanonicalApprovalDigest(requested.WorkspaceID, requested.PluginID,
		requested.ConversationKey, requested.TargetTaskID, requested.RepositoryID, requested.Provider, requested.Purpose)
	decision := a.plugins.AuthorizeCapability(record.InstallationID, requested.WorkspaceID,
		"host.v2.write:provider_access", approval.Revision, requestDigest,
		plugins.CanonicalApprovalDigest("provider-access/v1", "grant"))
	if !decision.Allowed {
		return "", provideraccess.ErrGrantUnavailable
	}
	return record.InstallationID, nil
}

func validGrantPluginRecord(record *pluginstore.Record, requested provideraccess.GrantScope) bool {
	return record != nil && record.Status == pluginstore.StatusActive &&
		record.ID == requested.PluginID && record.InstallationID != "" &&
		record.Capabilities.CanWrite("provider_access") &&
		(requested.PluginInstallationID == "" || requested.PluginInstallationID == record.InstallationID)
}

func validGrantApproval(approval plugins.CapabilityApprovalDTO, installationID, workspaceID string) bool {
	return approval.State == string(plugins.ApprovalStateActive) &&
		approval.InstallationID == installationID && approval.WorkspaceID == workspaceID &&
		approval.Revision > 0
}

func (a *providerGrantAuthority) verifyGrantTarget(ctx context.Context, scope provideraccess.GrantScope) error {
	if err := a.verifyGrantTaskRepository(ctx, scope); err != nil {
		return err
	}
	return a.verifyGrantAppConnection(ctx, scope.WorkspaceID)
}

func (a *providerGrantAuthority) verifyGrantTaskRepository(ctx context.Context, scope provideraccess.GrantScope) error {
	task, err := a.tasks.GetTask(ctx, scope.TargetTaskID)
	if err != nil || task == nil || task.WorkspaceID != scope.WorkspaceID || task.ArchivedAt != nil {
		return provideraccess.ErrGrantUnavailable
	}
	if !hasGrantRepositoryAttachment(task, scope.RepositoryID) {
		return provideraccess.ErrGrantUnavailable
	}
	repo, err := a.tasks.GetRepository(ctx, scope.RepositoryID)
	if err != nil || !validGrantRepository(repo, scope.WorkspaceID) {
		return provideraccess.ErrGrantUnavailable
	}
	return nil
}

func hasGrantRepositoryAttachment(task *models.Task, repositoryID string) bool {
	for _, relation := range task.Repositories {
		if relation != nil && relation.RepositoryID == repositoryID {
			return true
		}
	}
	return false
}

func validGrantRepository(repo *models.Repository, workspaceID string) bool {
	return repo != nil && repo.WorkspaceID == workspaceID && repo.DeletedAt == nil &&
		repo.Provider == taskChangeProviderGitHub && repo.ProviderHost == gitCredentialGitHubHost &&
		repo.ProviderOwner != "" && repo.ProviderName != ""
}

func (a *providerGrantAuthority) verifyGrantAppConnection(ctx context.Context, workspaceID string) error {
	connection, err := a.connections.GetWorkspaceConnection(ctx, workspaceID)
	if err != nil || connection == nil || connection.WorkspaceID != workspaceID ||
		connection.Source != github.ConnectionSourceGitHubAppInstallation ||
		connection.Status != github.ConnectionStatusActive || connection.InstallationID == nil ||
		*connection.InstallationID <= 0 || connection.AppRegistrationID == "" ||
		connection.GitHubHost != gitCredentialGitHubHost {
		return provideraccess.ErrGrantUnavailable
	}
	return nil
}

func validRequestedGrantScope(scope provideraccess.GrantScope) bool {
	if scope.Provider != taskChangeProviderGitHub || scope.Purpose != "actions_write" {
		return false
	}
	for _, value := range []string{scope.PluginID, scope.WorkspaceID, scope.ConversationKey,
		scope.TargetTaskID, scope.RepositoryID} {
		if value == "" || value != strings.TrimSpace(value) {
			return false
		}
	}
	return true
}
