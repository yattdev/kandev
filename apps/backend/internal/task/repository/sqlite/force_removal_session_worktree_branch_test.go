package sqlite

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

func TestClaimForceRemovalBlocksSessionWorktreeBranchCacheUpdates(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-branch-cache-ws", Name: "Force"}))
	for _, taskID := range []string{"force-branch-cache-held", "force-branch-cache-foreign"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: "force-branch-cache-ws", Title: taskID}))
		environmentID := taskID + "-environment"
		require.NoError(t, repo.CreateTaskEnvironment(ctx, &models.TaskEnvironment{
			ID: environmentID, TaskID: taskID, ExecutorType: string(models.ExecutorTypeLocal), Status: models.TaskEnvironmentStatusReady,
		}))
		require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{
			ID: taskID + "-session", TaskID: taskID, TaskEnvironmentID: environmentID,
		}))
		for _, suffix := range []string{"all", "repository", "worktree"} {
			require.NoError(t, repo.CreateTaskEnvironmentRepo(ctx, &models.TaskEnvironmentRepo{
				ID: taskID + "-" + suffix, TaskEnvironmentID: environmentID, RepositoryID: suffix,
				WorktreeID: suffix, WorktreeBranch: "main", Status: "active",
			}))
		}
	}

	heldTask, err := repo.GetTask(ctx, "force-branch-cache-held")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{
		TaskID: heldTask.ID, WorkspaceID: heldTask.WorkspaceID, TaskGeneration: heldTask.UpdatedAt,
		AdmissionGeneration: "admission", OperationID: "branch-cache", RequestDigest: "request", PreviewDigest: "preview",
	})
	require.NoError(t, err)

	require.ErrorIs(t, repo.UpdateTaskSessionWorktreeBranch(ctx, "force-branch-cache-held-session", "held-all"), ErrForceRemovalTaskHeld)
	require.ErrorIs(t, repo.UpdateTaskSessionWorktreeBranchByRepository(ctx, "force-branch-cache-held-session", "repository", "held-repository"), ErrForceRemovalTaskHeld)
	require.ErrorIs(t, repo.UpdateTaskSessionWorktreeBranchByWorktree(ctx, "force-branch-cache-held-session", "worktree", "held-worktree"), ErrForceRemovalTaskHeld)
	heldRepos, err := repo.ListTaskEnvironmentRepos(ctx, "force-branch-cache-held-environment")
	require.NoError(t, err)
	for _, environmentRepo := range heldRepos {
		require.Equal(t, "main", environmentRepo.WorktreeBranch)
	}

	require.NoError(t, repo.UpdateTaskSessionWorktreeBranch(ctx, "force-branch-cache-foreign-session", "foreign-all"))
	require.NoError(t, repo.UpdateTaskSessionWorktreeBranchByRepository(ctx, "force-branch-cache-foreign-session", "repository", "foreign-repository"))
	require.NoError(t, repo.UpdateTaskSessionWorktreeBranchByWorktree(ctx, "force-branch-cache-foreign-session", "worktree", "foreign-worktree"))
	foreignRepos, err := repo.ListTaskEnvironmentRepos(ctx, "force-branch-cache-foreign-environment")
	require.NoError(t, err)
	branches := map[string]string{}
	for _, environmentRepo := range foreignRepos {
		branches[environmentRepo.ID] = environmentRepo.WorktreeBranch
	}
	require.Equal(t, "foreign-all", branches["force-branch-cache-foreign-all"])
	require.Equal(t, "foreign-repository", branches["force-branch-cache-foreign-repository"])
	require.Equal(t, "foreign-worktree", branches["force-branch-cache-foreign-worktree"])
}
