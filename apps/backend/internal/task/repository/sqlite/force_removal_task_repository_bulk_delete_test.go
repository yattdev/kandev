package sqlite

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

func TestClaimForceRemovalBlocksTaskRepositoryBulkDelete(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-task-repository-bulk-delete-ws", Name: "Force"}))
	for _, taskID := range []string{"force-task-repository-bulk-delete-held", "force-task-repository-bulk-delete-foreign"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: "force-task-repository-bulk-delete-ws", Title: taskID}))
		repositoryID := taskID + "-repository"
		require.NoError(t, repo.CreateRepository(ctx, &models.Repository{ID: repositoryID, WorkspaceID: "force-task-repository-bulk-delete-ws", Name: repositoryID, SourceType: "local", LocalPath: "/tmp/" + repositoryID, DefaultBranch: "main"}))
		require.NoError(t, repo.CreateTaskRepository(ctx, &models.TaskRepository{ID: taskID + "-link", TaskID: taskID, RepositoryID: repositoryID, BaseBranch: "main"}))
	}
	heldTask, err := repo.GetTask(ctx, "force-task-repository-bulk-delete-held")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: heldTask.ID, WorkspaceID: heldTask.WorkspaceID, TaskGeneration: heldTask.UpdatedAt, AdmissionGeneration: "admission", OperationID: "task-repository-bulk-delete", RequestDigest: "request", PreviewDigest: "preview"})
	require.NoError(t, err)
	require.ErrorIs(t, repo.DeleteTaskRepositoriesByTask(ctx, heldTask.ID), ErrForceRemovalTaskHeld)
	links, err := repo.ListTaskRepositories(ctx, heldTask.ID)
	require.NoError(t, err)
	require.Len(t, links, 1)
	require.NoError(t, repo.DeleteTaskRepositoriesByTask(ctx, "force-task-repository-bulk-delete-foreign"))
	links, err = repo.ListTaskRepositories(ctx, "force-task-repository-bulk-delete-foreign")
	require.NoError(t, err)
	require.Empty(t, links)
	require.NoError(t, repo.DeleteTaskRepositoriesByTask(ctx, "force-task-repository-bulk-delete-missing"))
}
