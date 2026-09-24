package lifecycle

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/kandev/kandev/internal/agent/docker"
	"github.com/kandev/kandev/internal/worktree"
)

const gitMetadataProjectionInvalid = "git_metadata_projection_invalid"

var containerGitDirMu sync.Mutex

func projectionsFromPrepareResult(result *EnvPrepareResult) ([]*worktree.GitMetadataProjection, error) {
	if result == nil {
		return nil, nil
	}
	projections := make([]*worktree.GitMetadataProjection, 0, len(result.Worktrees)+1)
	if len(result.Worktrees) == 0 && result.GitMetadataProjection != nil {
		projections = append(projections, result.GitMetadataProjection)
	}
	for _, prepared := range result.Worktrees {
		if prepared.GitMetadataProjection == nil {
			return nil, errors.New(gitMetadataProjectionInvalid)
		}
		projections = append(projections, prepared.GitMetadataProjection)
	}
	seen := make(map[string]struct{}, len(projections))
	for _, projection := range projections {
		if projection == nil || projection.Revalidate() != nil {
			return nil, errors.New(gitMetadataProjectionInvalid)
		}
		if _, exists := seen[projection.CheckoutPath]; exists {
			return nil, fmt.Errorf("%s: duplicate checkout", gitMetadataProjectionInvalid)
		}
		seen[projection.CheckoutPath] = struct{}{}
	}
	return projections, nil
}

// gitMetadataMounts exposes task-owned worktree metadata as writable while
// keeping source repository metadata read-only.
func gitMetadataMounts(projections []*worktree.GitMetadataProjection, workspacePath string) ([]docker.MountConfig, error) {
	if len(projections) == 0 {
		return nil, nil
	}
	common := make(map[string]struct{}, len(projections))
	writable := make(map[string]struct{}, len(projections))
	for _, projection := range projections {
		if projection == nil || projection.Revalidate() != nil {
			return nil, errors.New(gitMetadataProjectionInvalid)
		}
		common[projection.SharedCommonDir] = struct{}{}
		containerCheckoutPath, err := containerCheckoutPath(workspacePath, projection.CheckoutPath)
		if err != nil {
			return nil, err
		}
		if err := prepareContainerGitDir(projection, containerCheckoutPath); err != nil {
			return nil, err
		}
		writable[projection.GitDir] = struct{}{}
	}
	for _, projection := range projections {
		if err := projection.Revalidate(); err != nil {
			return nil, errors.New(gitMetadataProjectionInvalid)
		}
		if err := validateContainerGitDir(projection.CommonDir); err != nil {
			return nil, err
		}
	}

	mounts := make([]docker.MountConfig, 0, len(common)+len(writable))
	for _, path := range sortedGitMetadataPaths(common) {
		mounts = append(mounts, docker.MountConfig{Source: path, Target: path, ReadOnly: true})
	}
	for _, path := range sortedGitMetadataPathSet(writable) {
		mounts = append(mounts, docker.MountConfig{Source: path, Target: path})
	}
	return mounts, nil
}

func prepareContainerGitDir(projection *worktree.GitMetadataProjection, containerCheckoutPath string) error {
	containerGitDirMu.Lock()
	defer containerGitDirMu.Unlock()

	privateGitDir := filepath.Join(projection.GitDir, "kandev-agent-git")
	info, err := os.Lstat(privateGitDir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := initializeContainerGitDir(projection, privateGitDir, containerCheckoutPath); err != nil {
			return err
		}
	case err != nil:
		return err
	case info.Mode()&os.ModeSymlink != 0 || !info.IsDir():
		return errors.New(gitMetadataProjectionInvalid)
	}
	if err := configureContainerGitDir(projection, privateGitDir, containerCheckoutPath); err != nil {
		return err
	}
	commondirPath := filepath.Join(projection.GitDir, "commondir")
	commondir, err := os.ReadFile(commondirPath)
	if err != nil {
		return err
	}
	resolvedCommon, err := filepath.Abs(filepath.Join(projection.GitDir, strings.TrimSpace(string(commondir))))
	if err != nil {
		return err
	}
	if filepath.Clean(resolvedCommon) != filepath.Clean(privateGitDir) {
		if err := projection.Revalidate(); err != nil {
			return errors.New(gitMetadataProjectionInvalid)
		}
		if err := replaceGitMetadataPointer(commondirPath, "kandev-agent-git\n"); err != nil {
			return err
		}
		if err := projection.Refresh(); err != nil {
			return err
		}
	} else if err := projection.Revalidate(); err != nil {
		return errors.New(gitMetadataProjectionInvalid)
	}
	if err := validateContainerGitDir(privateGitDir); err != nil {
		return err
	}
	return nil
}

func replaceGitMetadataPointer(path, content string) (resultErr error) {
	temp, err := os.CreateTemp(filepath.Dir(path), ".commondir-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() {
		if cleanupErr := os.Remove(tempPath); cleanupErr != nil && !errors.Is(cleanupErr, os.ErrNotExist) {
			resultErr = errors.Join(resultErr, cleanupErr)
		}
	}()
	if _, err := temp.WriteString(content); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func initializeContainerGitDir(projection *worktree.GitMetadataProjection, privateGitDir, containerCheckoutPath string) (resultErr error) {
	stagingDir, err := os.MkdirTemp(projection.GitDir, ".kandev-agent-git-")
	if err != nil {
		return err
	}
	defer func() {
		if cleanupErr := os.RemoveAll(stagingDir); cleanupErr != nil {
			resultErr = errors.Join(resultErr, cleanupErr)
		}
	}()
	stagingGitDir := filepath.Join(stagingDir, "metadata")
	command := exec.Command("git", "clone", "--shared", "--bare", projection.CheckoutPath, stagingGitDir)
	if output, cloneErr := command.CombinedOutput(); cloneErr != nil {
		return fmt.Errorf("initialize task Git metadata: %w: %s", cloneErr, strings.TrimSpace(string(output)))
	}
	if err := configureContainerGitDir(projection, stagingGitDir, containerCheckoutPath); err != nil {
		return err
	}
	return os.Rename(stagingGitDir, privateGitDir)
}

func configureContainerGitDir(projection *worktree.GitMetadataProjection, privateGitDir, containerCheckoutPath string) error {
	for _, args := range [][]string{
		{"--git-dir", privateGitDir, "config", "core.bare", "false"},
		{"--git-dir", privateGitDir, "config", "core.worktree", containerCheckoutPath},
		{"--git-dir", privateGitDir, "config", "core.logAllRefUpdates", "true"},
	} {
		if output, configErr := exec.Command("git", args...).CombinedOutput(); configErr != nil {
			return fmt.Errorf("configure task Git metadata: %w: %s", configErr, strings.TrimSpace(string(output)))
		}
	}
	keys := []string{"user.name", "user.email"}
	if strings.HasPrefix(projection.CurrentRef, "refs/heads/") {
		branch := strings.TrimPrefix(projection.CurrentRef, "refs/heads/")
		keys = append(keys, "branch."+branch+".remote", "branch."+branch+".merge")
	}
	for _, key := range keys {
		if err := copyGitConfigValue(projection.CheckoutPath, privateGitDir, key); err != nil {
			return err
		}
	}
	remoteURL, err := exec.Command("git", "-C", projection.CheckoutPath, "remote", "get-url", "origin").Output()
	if err == nil {
		if output, configErr := exec.Command("git", "--git-dir", privateGitDir, "remote", "set-url", "origin", strings.TrimSpace(string(remoteURL))).CombinedOutput(); configErr != nil {
			return fmt.Errorf("configure task Git remote: %w: %s", configErr, strings.TrimSpace(string(output)))
		}
	}
	return nil
}

func copyGitConfigValue(checkoutPath, gitDir, key string) error {
	value, err := exec.Command("git", "-C", checkoutPath, "config", "--get", key).Output()
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) && exitError.ExitCode() == 1 {
			return nil
		}
		return fmt.Errorf("read task Git config %q: %w", key, err)
	}
	args := []string{"--git-dir", gitDir, "config", "--replace-all", key, strings.TrimSpace(string(value))}
	if output, err := exec.Command("git", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("copy task Git config %q: %w: %s", key, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func validateContainerGitDir(path string) error {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New(gitMetadataProjectionInvalid)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || filepath.Clean(resolved) != filepath.Clean(path) {
		return errors.New(gitMetadataProjectionInvalid)
	}
	return nil
}

func containerCheckoutPath(workspacePath, checkoutPath string) (string, error) {
	if workspacePath == "" {
		return spritesWorkspacePath, nil
	}
	workspace, err := filepath.Abs(workspacePath)
	if err != nil {
		return "", err
	}
	checkout, err := filepath.Abs(checkoutPath)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(workspace, checkout)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New(gitMetadataProjectionInvalid)
	}
	if relative == "." {
		return spritesWorkspacePath, nil
	}
	return filepath.Join(spritesWorkspacePath, relative), nil
}

func sortedGitMetadataPaths(paths map[string]struct{}) []string {
	result := make([]string, 0, len(paths))
	for path := range paths {
		result = append(result, path)
	}
	sort.Strings(result)
	return result
}

func sortedGitMetadataPathSet(mounts map[string]struct{}) []string {
	result := make([]string, 0, len(mounts))
	for path := range mounts {
		result = append(result, path)
	}
	sort.Strings(result)
	return result
}
