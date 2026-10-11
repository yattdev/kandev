package executor

import (
	"context"
	"errors"
	"fmt"

	"github.com/kandev/kandev/internal/task/models"
)

type hostRepositoryPathInspector interface {
	InspectHostRepositoryPath(context.Context, *models.Repository, string) (bool, error)
	WorkspaceProviderRepositoryPath(string, string, string, string, string, string, string) (string, error)
}

func (e *Executor) materializeManagedHostRepository(ctx context.Context, taskID, sessionID string, repo *models.Repository) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	inspector, ok := e.repoCloner.(hostRepositoryPathInspector)
	if !ok {
		return "", errors.New("managed host repository path validation is unavailable")
	}
	path := repo.LocalPath
	if path == "" {
		var err error
		path, err = inspector.WorkspaceProviderRepositoryPath(repo.WorkspaceID, repo.Provider, repo.ProviderHost, repo.ProviderScope, repo.ProviderRepoID, repo.ProviderOwner, repo.ProviderName)
		if err != nil {
			return "", err
		}
	}
	exists, err := inspector.InspectHostRepositoryPath(ctx, repo, path)
	if err != nil {
		return "", err
	}
	if exists {
		return e.persistHostRepositoryPath(ctx, repo, path)
	}
	if taskID == "" || sessionID == "" {
		return "", errors.New("task and session identity are required for managed host cloning")
	}
	// Validate the destination as well as the recorded legacy path before clone
	// authentication or filesystem mutation can occur.
	if _, err := inspector.InspectHostRepositoryPath(ctx, repo, ""); err != nil {
		return "", err
	}
	path, err = e.cloneManagedHostRepository(ctx, taskID, sessionID, repo, inspector)
	if err != nil {
		return "", err
	}
	return e.persistHostRepositoryPath(ctx, repo, path)
}

func (e *Executor) cloneManagedHostRepository(ctx context.Context, taskID, sessionID string, repo *models.Repository, inspector hostRepositoryPathInspector) (string, error) {
	var err error
	cloneURL := repo.RemoteURL
	if cloneURL == "" {
		cloneURL, err = e.repoCloner.BuildCloneURLWithHost(ctx, repo.Provider, repo.ProviderHost, repo.ProviderOwner, repo.ProviderName)
		if err != nil {
			return "", err
		}
	}
	if cloneURL == "" {
		return "", ErrNoCloneURL
	}
	path, _, err := e.ensureClonedWithWorkspaceAuthForSessionAndState(ctx, taskID, sessionID, repo, cloneURL)
	if err != nil {
		return "", err
	}
	if path == "" {
		return "", errors.New("managed repository clone produced no local path")
	}
	exists, err := inspector.InspectHostRepositoryPath(ctx, repo, path)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", errors.New("managed repository clone checkout is missing")
	}
	return path, nil
}

func (e *Executor) persistHostRepositoryPath(ctx context.Context, repo *models.Repository, path string) (string, error) {
	if path != repo.LocalPath {
		if e.repoUpdater == nil {
			return "", errors.New("managed repository path persistence is unavailable")
		}
		if err := e.repoUpdater.UpdateRepositoryLocalPath(ctx, repo.ID, path); err != nil {
			return "", fmt.Errorf("persist managed repository local path: %w", err)
		}
		repo.LocalPath = path
	}
	return path, nil
}
