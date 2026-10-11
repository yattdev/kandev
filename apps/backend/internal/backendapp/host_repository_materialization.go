package backendapp

import (
	"context"
	"fmt"

	"github.com/kandev/kandev/internal/orchestrator"
	"github.com/kandev/kandev/internal/task/models"
)

const (
	hostRepositorySourceLocal    = "local"
	hostRepositorySourceProvider = "provider"
)

func hasHostProviderIdentity(repository *models.Repository) bool {
	return (repository.ProviderOwner != "" && repository.ProviderName != "") || (repository.ProviderScope != "" && repository.ProviderRepoID != "")
}

// SetHostRepositoryCloner binds the configured task/session-scoped credential
// seam to direct branch attachment, including nested batch materialization.
func (b *branchMaterializer) SetHostRepositoryCloner(cloner orchestrator.RepositoryHostCloner) {
	if b != nil {
		b.hostCloner = cloner
	}
}

func (b *branchMaterializer) ensureHostRepositoryPath(ctx context.Context, taskID, sessionID string, repository *models.Repository) error {
	if repository.SourceType == hostRepositorySourceLocal || (repository.LocalPath != "" && repository.SourceType != hostRepositorySourceProvider) {
		return nil
	}
	if !hasHostProviderIdentity(repository) {
		return fmt.Errorf("repository %q source path is missing", repository.ID)
	}
	if b.hostCloner == nil {
		return fmt.Errorf("host repository cloner is unavailable for %q", repository.Name)
	}
	path, err := b.hostCloner.EnsureRepositoryClonedForSession(ctx, taskID, sessionID, repository)
	if err != nil {
		return fmt.Errorf("clone repository %q: %w", repository.Name, err)
	}
	if path == "" {
		return fmt.Errorf("repository %q clone produced no local path", repository.Name)
	}
	repository.LocalPath = path
	return nil
}
