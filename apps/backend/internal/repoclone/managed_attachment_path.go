package repoclone

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kandev/kandev/internal/common/subproc"
	"github.com/kandev/kandev/internal/task/models"
)

// InspectHostRepositoryPath admits only exact paths derived from the registered
// provider identity. Missing managed paths may be cloned; occupied or foreign
// paths are never repaired by deleting, redirecting, or rewriting their origin.
func (c *Cloner) InspectHostRepositoryPath(ctx context.Context, repo *models.Repository, path string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if repo == nil || repo.SourceType != "provider" {
		return false, ErrRepositoryOwnershipMismatch
	}
	destination, err := c.WorkspaceProviderRepositoryPath(repo.WorkspaceID, repo.Provider, repo.ProviderHost, repo.ProviderScope, repo.ProviderRepoID, repo.ProviderOwner, repo.ProviderName)
	if err != nil {
		return false, err
	}
	if path == "" {
		path = destination
	}
	if !filepath.IsAbs(path) {
		return false, ErrRepositoryOwnershipMismatch
	}
	if filepath.Clean(path) != filepath.Clean(destination) {
		if !c.isLegacyAttachmentPath(repo, path) {
			return false, ErrRepositoryOwnershipMismatch
		}
	}
	if err := checkAttachmentPathAncestors(path); err != nil {
		return false, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		return false, ErrRepositoryOwnershipMismatch
	}
	if err := checkAttachmentGitDirectory(path); err != nil {
		return false, err
	}
	return true, c.verifyAttachmentCheckout(ctx, repo, path)
}

func (c *Cloner) isLegacyAttachmentPath(repo *models.Repository, path string) bool {
	// Scoped plugin identities cannot share the pre-workspace legacy layouts.
	provider := strings.ToLower(repo.Provider)
	if repo.ProviderScope != "" || (provider != githubProvider && provider != gitlabProvider) {
		return false
	}
	providerPath, err := c.ProviderRepoPath(provider, repo.ProviderHost, repo.ProviderOwner, repo.ProviderName)
	if err != nil {
		return false
	}
	if filepath.Clean(path) == filepath.Clean(providerPath) {
		return true
	}
	// Older built-in clones also used owner/name without provider or workspace
	// directories. The registered path is exact; an existing checkout still has
	// to prove its provider origin before it can be reused.
	legacy, err := c.RepoPath(repo.ProviderOwner, repo.ProviderName)
	return err == nil && filepath.Clean(path) == filepath.Clean(legacy)
}

func checkAttachmentPathAncestors(path string) error {
	for current := filepath.Clean(path); ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
			return ErrRepositoryOwnershipMismatch
		}
		if filepath.Dir(current) == current {
			return nil
		}
	}
}

func checkAttachmentGitDirectory(path string) error {
	gitDir := filepath.Join(path, ".git")
	info, err := os.Lstat(gitDir)
	if err != nil {
		return fmt.Errorf("inspect managed Git directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ErrRepositoryOwnershipMismatch
	}
	// A managed clone owns its Git common directory. A commondir file redirects
	// repository operations even when .git itself is an ordinary directory.
	if _, err := os.Lstat(filepath.Join(gitDir, "commondir")); !errors.Is(err, os.ErrNotExist) {
		if err != nil {
			return err
		}
		return ErrRepositoryOwnershipMismatch
	}
	return checkAttachmentGitEntries(gitDir)
}

func (c *Cloner) verifyAttachmentCheckout(ctx context.Context, repo *models.Repository, path string) error {
	cmd := subproc.NewGitCommand(ctx, "-C", path, "rev-parse", "--path-format=absolute", "--show-toplevel", "--absolute-git-dir", "--git-common-dir")
	cleanup, err := configureGitCommand(cmd, nil)
	if err != nil {
		return err
	}
	output, err := subproc.RunGitCombinedOutputClass(ctx, subproc.GitLifecycle, cmd)
	cleanup()
	if err != nil {
		return formatGitOriginError(path, output, err)
	}
	paths := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(paths) != 3 || filepath.Clean(paths[0]) != filepath.Clean(path) ||
		filepath.Clean(paths[1]) != filepath.Join(filepath.Clean(path), ".git") || paths[1] != paths[2] {
		return ErrRepositoryOwnershipMismatch
	}
	want := strings.TrimSpace(repo.RemoteURL)
	if want == "" {
		want, err = c.BuildCloneURLWithHost(ctx, repo.Provider, repo.ProviderHost, repo.ProviderOwner, repo.ProviderName)
		if err != nil {
			return err
		}
	}
	got, err := c.originURLLocked(ctx, path)
	if err != nil {
		return err
	}
	if attachmentOriginIdentity(got) != attachmentOriginIdentity(want) {
		return ErrManagedCloneOriginMismatch
	}
	return nil
}

func attachmentOriginIdentity(origin string) string {
	if https := CanonicalHTTPSCloneURL(origin); https != "" {
		origin = https
	}
	return strings.TrimSuffix(strings.TrimSuffix(origin, "/"), ".git")
}

func checkAttachmentGitEntries(gitDir string) error {
	for _, entry := range []string{"objects", "objects/info", "refs", "config", "HEAD"} {
		info, err := os.Lstat(filepath.Join(gitDir, filepath.FromSlash(entry)))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return ErrRepositoryOwnershipMismatch
		}
	}
	if _, err := os.Lstat(filepath.Join(gitDir, "objects", "info", "alternates")); !errors.Is(err, os.ErrNotExist) {
		if err != nil {
			return err
		}
		return ErrRepositoryOwnershipMismatch
	}
	return nil
}
