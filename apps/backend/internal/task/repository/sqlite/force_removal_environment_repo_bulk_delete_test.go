package sqlite

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

func TestClaimForceRemovalBlocksEnvironmentRepoBulkDelete(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-environment-repo-bulk-delete-ws", Name: "Force"}))

	createEnvironmentRepos := func(taskID string) string {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: "force-environment-repo-bulk-delete-ws", Title: taskID}))
		environmentID := taskID + "-environment"
		require.NoError(t, repo.CreateTaskEnvironment(ctx, &models.TaskEnvironment{ID: environmentID, TaskID: taskID, ExecutorType: string(models.ExecutorTypeLocal), Status: models.TaskEnvironmentStatusCreating}))
		for _, repositoryID := range []string{"repository-a", "repository-b"} {
			require.NoError(t, repo.CreateTaskEnvironmentRepo(ctx, &models.TaskEnvironmentRepo{ID: taskID + "-" + repositoryID, TaskEnvironmentID: environmentID, RepositoryID: repositoryID, Status: "active"}))
		}
		return environmentID
	}

	heldEnvironmentID := createEnvironmentRepos("force-environment-repo-bulk-delete-held")
	foreignEnvironmentID := createEnvironmentRepos("force-environment-repo-bulk-delete-foreign")
	heldTask, err := repo.GetTask(ctx, "force-environment-repo-bulk-delete-held")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{
		TaskID: heldTask.ID, WorkspaceID: heldTask.WorkspaceID, TaskGeneration: heldTask.UpdatedAt,
		AdmissionGeneration: "admission", OperationID: "environment-repo-bulk-delete", RequestDigest: "request", PreviewDigest: "preview",
	})
	require.NoError(t, err)

	require.ErrorIs(t, repo.DeleteTaskEnvironmentReposByEnv(ctx, heldEnvironmentID), ErrForceRemovalTaskHeld)
	heldRepos, err := repo.ListTaskEnvironmentRepos(ctx, heldEnvironmentID)
	require.NoError(t, err)
	require.Len(t, heldRepos, 2)

	require.NoError(t, repo.DeleteTaskEnvironmentReposByEnv(ctx, foreignEnvironmentID))
	foreignRepos, err := repo.ListTaskEnvironmentRepos(ctx, foreignEnvironmentID)
	require.NoError(t, err)
	require.Empty(t, foreignRepos)
	require.NoError(t, repo.DeleteTaskEnvironmentReposByEnv(ctx, "force-environment-repo-bulk-delete-missing"))
}
