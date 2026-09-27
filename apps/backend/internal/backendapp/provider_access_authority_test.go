package backendapp

import (
	"context"
	"errors"
	"testing"

	"github.com/kandev/kandev/internal/authz"
	"github.com/kandev/kandev/internal/github"
	"github.com/kandev/kandev/internal/plugins"
	"github.com/kandev/kandev/internal/plugins/manifest"
	pluginstore "github.com/kandev/kandev/internal/plugins/store"
	"github.com/kandev/kandev/internal/provideraccess"
	"github.com/kandev/kandev/internal/task/models"
)

type grantTaskReaderStub struct {
	task *models.Task
	repo *models.Repository
}

func (s *grantTaskReaderStub) AuthorizeWorkspaceScope(context.Context, string, authz.Scope) error {
	return nil
}

func (s *grantTaskReaderStub) GetTask(context.Context, string) (*models.Task, error) {
	return s.task, nil
}

func (s *grantTaskReaderStub) GetRepository(context.Context, string) (*models.Repository, error) {
	return s.repo, nil
}

type grantPluginReaderStub struct {
	record   *pluginstore.Record
	approval plugins.CapabilityApprovalDTO
	allowed  bool
}

func (s *grantPluginReaderStub) Get(string) (*pluginstore.Record, error) {
	return s.record, nil
}

func (s *grantPluginReaderStub) GetCapabilityApproval(string, string) (plugins.CapabilityApprovalDTO, bool, error) {
	return s.approval, true, nil
}

func (s *grantPluginReaderStub) AuthorizeCapability(
	installationID, workspaceID, capabilityID string, revision uint64, requestDigest, methodDigest string,
) plugins.ApprovalDecision {
	return plugins.ApprovalDecision{Allowed: s.allowed &&
		installationID == s.record.InstallationID && workspaceID == s.approval.WorkspaceID &&
		capabilityID == "host.v2.write:provider_access" && revision == s.approval.Revision &&
		requestDigest != "" && methodDigest != ""}
}

type grantConnectionReaderStub struct{ connection *github.WorkspaceConnection }

func (s *grantConnectionReaderStub) GetWorkspaceConnection(context.Context, string) (*github.WorkspaceConnection, error) {
	return s.connection, nil
}

func newGrantAuthorityFixture() (*providerGrantAuthority, provideraccess.GrantScope,
	*grantTaskReaderStub, *grantPluginReaderStub, *grantConnectionReaderStub) {
	installationID := int64(42)
	scope := provideraccess.GrantScope{
		PluginID: "coordinator", WorkspaceID: "workspace-1", ConversationKey: "conversation-1",
		TargetTaskID: "target-1", RepositoryID: "repository-row-1", Provider: "github", Purpose: "actions_write",
	}
	tasks := &grantTaskReaderStub{
		task: &models.Task{ID: "target-1", WorkspaceID: "workspace-1",
			Repositories: []*models.TaskRepository{{RepositoryID: "repository-row-1"}}},
		repo: &models.Repository{ID: "repository-row-1", WorkspaceID: "workspace-1",
			Provider: "github", ProviderHost: "github.com", ProviderOwner: "owner", ProviderName: "repo"},
	}
	installed := &grantPluginReaderStub{
		record: &pluginstore.Record{Manifest: manifest.Manifest{ID: "coordinator",
			Capabilities: manifest.Capabilities{APIWrite: []string{"provider_access"}}},
			InstallationID: "plugin-installation-1", Status: pluginstore.StatusActive},
		approval: plugins.CapabilityApprovalDTO{InstallationID: "plugin-installation-1",
			WorkspaceID: "workspace-1", Revision: 7, State: string(plugins.ApprovalStateActive)},
		allowed: true,
	}
	connections := &grantConnectionReaderStub{connection: &github.WorkspaceConnection{
		WorkspaceID: "workspace-1", Source: github.ConnectionSourceGitHubAppInstallation,
		Status: github.ConnectionStatusActive, InstallationID: &installationID,
		AppRegistrationID: "app-registration-1", GitHubHost: "github.com",
	}}
	return &providerGrantAuthority{tasks: tasks, plugins: installed, connections: connections},
		scope, tasks, installed, connections
}

func TestProviderGrantAuthorityDerivesInstallationAndChecksLiveScope(t *testing.T) {
	authority, scope, _, _, _ := newGrantAuthorityFixture()
	resolved, err := authority.ResolveGrantScope(context.Background(), scope)
	if err != nil || resolved.PluginInstallationID != "plugin-installation-1" {
		t.Fatalf("resolved grant scope = %+v, err = %v", resolved, err)
	}
}

func TestProviderGrantAuthorityDeniesStaleOrForeignScope(t *testing.T) {
	cases := map[string]func(*provideraccess.GrantScope, *grantTaskReaderStub, *grantPluginReaderStub, *grantConnectionReaderStub){
		"caller-selected installation": func(s *provideraccess.GrantScope, _ *grantTaskReaderStub, _ *grantPluginReaderStub, _ *grantConnectionReaderStub) {
			s.PluginInstallationID = "other-installation"
		},
		"inactive plugin": func(_ *provideraccess.GrantScope, _ *grantTaskReaderStub, p *grantPluginReaderStub, _ *grantConnectionReaderStub) {
			p.record.Status = pluginstore.StatusDisabled
		},
		"missing H6 approval": func(_ *provideraccess.GrantScope, _ *grantTaskReaderStub, p *grantPluginReaderStub, _ *grantConnectionReaderStub) {
			p.allowed = false
		},
		"foreign task": func(_ *provideraccess.GrantScope, r *grantTaskReaderStub, _ *grantPluginReaderStub, _ *grantConnectionReaderStub) {
			r.task.WorkspaceID = "workspace-2"
		},
		"detached repository": func(_ *provideraccess.GrantScope, r *grantTaskReaderStub, _ *grantPluginReaderStub, _ *grantConnectionReaderStub) {
			r.task.Repositories = nil
		},
		"foreign repository": func(_ *provideraccess.GrantScope, r *grantTaskReaderStub, _ *grantPluginReaderStub, _ *grantConnectionReaderStub) {
			r.repo.WorkspaceID = "workspace-2"
		},
		"PAT connection": func(_ *provideraccess.GrantScope, _ *grantTaskReaderStub, _ *grantPluginReaderStub, c *grantConnectionReaderStub) {
			c.connection.Source = github.ConnectionSourcePAT
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			authority, scope, tasks, installed, connections := newGrantAuthorityFixture()
			mutate(&scope, tasks, installed, connections)
			if _, err := authority.ResolveGrantScope(context.Background(), scope); !errors.Is(err, provideraccess.ErrGrantUnavailable) {
				t.Fatalf("stale or foreign grant scope error = %v", err)
			}
		})
	}
}
