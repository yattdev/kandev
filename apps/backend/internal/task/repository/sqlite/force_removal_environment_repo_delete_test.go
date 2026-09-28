package sqlite

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

func TestClaimForceRemovalBlocksEnvironmentRepoDelete(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-environment-repo-delete-ws", Name: "Force"}))

	createRepo := func(taskID string) *models.TaskEnvironmentRepo {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: "force-environment-repo-delete-ws", Title: taskID}))
		environmentID := taskID + "-environment"
		require.NoError(t, repo.CreateTaskEnvironment(ctx, &models.TaskEnvironment{ID: environmentID, TaskID: taskID, ExecutorType: string(models.ExecutorTypeLocal), Status: models.TaskEnvironmentStatusCreating}))
		environmentRepo := &models.TaskEnvironmentRepo{ID: taskID + "-repo", TaskEnvironmentID: environmentID, RepositoryID: "repository", Status: "active"}
		require.NoError(t, repo.CreateTaskEnvironmentRepo(ctx, environmentRepo))
		return environmentRepo
	}

	heldRepo := createRepo("force-environment-repo-delete-held")
	foreignRepo := createRepo("force-environment-repo-delete-foreign")
	heldTask, err := repo.GetTask(ctx, "force-environment-repo-delete-held")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{
		TaskID: heldTask.ID, WorkspaceID: heldTask.WorkspaceID, TaskGeneration: heldTask.UpdatedAt,
		AdmissionGeneration: "admission", OperationID: "environment-repo-delete", RequestDigest: "request", PreviewDigest: "preview",
	})
	require.NoError(t, err)

	require.ErrorIs(t, repo.DeleteTaskEnvironmentRepo(ctx, heldRepo.ID), ErrForceRemovalTaskHeld)
	heldRepos, err := repo.ListTaskEnvironmentRepos(ctx, heldRepo.TaskEnvironmentID)
	require.NoError(t, err)
	require.Len(t, heldRepos, 1)
	require.Equal(t, heldRepo.ID, heldRepos[0].ID)

	require.NoError(t, repo.DeleteTaskEnvironmentRepo(ctx, foreignRepo.ID))
	foreignRepos, err := repo.ListTaskEnvironmentRepos(ctx, foreignRepo.TaskEnvironmentID)
	require.NoError(t, err)
	require.Empty(t, foreignRepos)
	require.ErrorContains(t, repo.DeleteTaskEnvironmentRepo(ctx, "force-environment-repo-delete-missing"), "task environment repo not found")
}
