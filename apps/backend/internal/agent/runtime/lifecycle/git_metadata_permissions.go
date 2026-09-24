package lifecycle

import (
	"errors"
	"fmt"
	"sort"

	"github.com/kandev/kandev/internal/agent/docker"
	"github.com/kandev/kandev/internal/worktree"
)

const gitMetadataProjectionInvalid = "git_metadata_projection_invalid"

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

// gitMetadataMounts compiles validated worktree projections into layered
// mounts. The shared Git directory is read-only; only paths owned by the task
// checkout are reopened for Git's normal index, ref, reflog, and object writes.
func gitMetadataMounts(projections []*worktree.GitMetadataProjection) ([]docker.MountConfig, error) {
	if len(projections) == 0 {
		return nil, nil
	}
	common := make(map[string]struct{}, len(projections))
	writable := make(map[string]struct{}, len(projections)*8)
	for _, projection := range projections {
		if projection == nil || projection.Revalidate() != nil {
			return nil, errors.New(gitMetadataProjectionInvalid)
		}
		if projection.GitDir != projection.CommonDir {
			common[projection.CommonDir] = struct{}{}
		}
		for _, path := range projection.AgentWritablePaths {
			writable[path] = struct{}{}
		}
		for _, path := range projection.MountSupportPaths {
			writable[path] = struct{}{}
		}
	}

	mounts := make([]docker.MountConfig, 0, len(common)+len(writable))
	for _, path := range sortedGitMetadataPaths(common) {
		mounts = append(mounts, docker.MountConfig{Source: path, Target: path, ReadOnly: true})
	}
	for _, path := range sortedGitMetadataPaths(writable) {
		mounts = append(mounts, docker.MountConfig{Source: path, Target: path})
	}
	return mounts, nil
}

func sortedGitMetadataPaths(paths map[string]struct{}) []string {
	result := make([]string, 0, len(paths))
	for path := range paths {
		result = append(result, path)
	}
	sort.Strings(result)
	return result
}
