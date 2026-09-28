package sqlite

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

func TestClaimForceRemovalBlocksEnvironmentRepoUpdate(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-environment-repo-update-ws", Name: "Force"}))

	createRepo := func(taskID string) *models.TaskEnvironmentRepo {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: "force-environment-repo-update-ws", Title: taskID}))
		environmentID := taskID + "-environment"
		require.NoError(t, repo.CreateTaskEnvironment(ctx, &models.TaskEnvironment{ID: environmentID, TaskID: taskID, ExecutorType: string(models.ExecutorTypeLocal), Status: models.TaskEnvironmentStatusCreating}))
		environmentRepo := &models.TaskEnvironmentRepo{ID: taskID + "-repo", TaskEnvironmentID: environmentID, RepositoryID: "repository", WorktreeBranch: "main", Status: "active"}
		require.NoError(t, repo.CreateTaskEnvironmentRepo(ctx, environmentRepo))
		return environmentRepo
	}

	heldRepo := createRepo("force-environment-repo-update-held")
	foreignRepo := createRepo("force-environment-repo-update-foreign")
	heldTask, err := repo.GetTask(ctx, "force-environment-repo-update-held")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{
		TaskID: heldTask.ID, WorkspaceID: heldTask.WorkspaceID, TaskGeneration: heldTask.UpdatedAt,
		AdmissionGeneration: "admission", OperationID: "environment-repo-update", RequestDigest: "request", PreviewDigest: "preview",
	})
	require.NoError(t, err)

	heldRepo.WorktreeBranch = "held-after"
	require.ErrorIs(t, repo.UpdateTaskEnvironmentRepo(ctx, heldRepo), ErrForceRemovalTaskHeld)
	heldRepos, err := repo.ListTaskEnvironmentRepos(ctx, heldRepo.TaskEnvironmentID)
	require.NoError(t, err)
	require.Len(t, heldRepos, 1)
	require.Equal(t, "main", heldRepos[0].WorktreeBranch)

	foreignRepo.WorktreeBranch = "foreign-after"
	require.NoError(t, repo.UpdateTaskEnvironmentRepo(ctx, foreignRepo))
	foreignRepos, err := repo.ListTaskEnvironmentRepos(ctx, foreignRepo.TaskEnvironmentID)
	require.NoError(t, err)
	require.Len(t, foreignRepos, 1)
	require.Equal(t, "foreign-after", foreignRepos[0].WorktreeBranch)

	require.ErrorContains(t, repo.UpdateTaskEnvironmentRepo(ctx, &models.TaskEnvironmentRepo{ID: "force-environment-repo-update-missing"}), "task environment repo not found")
}
