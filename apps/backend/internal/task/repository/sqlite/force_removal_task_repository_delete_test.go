package sqlite

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

func TestClaimForceRemovalBlocksTaskRepositoryDelete(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-task-repository-delete-ws", Name: "Force"}))
	for _, taskID := range []string{"force-task-repository-delete-held", "force-task-repository-delete-foreign"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: "force-task-repository-delete-ws", Title: taskID}))
		repositoryID := taskID + "-repository"
		require.NoError(t, repo.CreateRepository(ctx, &models.Repository{ID: repositoryID, WorkspaceID: "force-task-repository-delete-ws", Name: repositoryID, SourceType: "local", LocalPath: "/tmp/" + repositoryID, DefaultBranch: "main"}))
		require.NoError(t, repo.CreateTaskRepository(ctx, &models.TaskRepository{ID: taskID + "-link", TaskID: taskID, RepositoryID: repositoryID, BaseBranch: "main"}))
	}
	heldTask, err := repo.GetTask(ctx, "force-task-repository-delete-held")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: heldTask.ID, WorkspaceID: heldTask.WorkspaceID, TaskGeneration: heldTask.UpdatedAt, AdmissionGeneration: "admission", OperationID: "task-repository-delete", RequestDigest: "request", PreviewDigest: "preview"})
	require.NoError(t, err)
	require.ErrorIs(t, repo.DeleteTaskRepository(ctx, "force-task-repository-delete-held-link"), ErrForceRemovalTaskHeld)
	_, err = repo.GetTaskRepository(ctx, "force-task-repository-delete-held-link")
	require.NoError(t, err)
	require.NoError(t, repo.DeleteTaskRepository(ctx, "force-task-repository-delete-foreign-link"))
	_, err = repo.GetTaskRepository(ctx, "force-task-repository-delete-foreign-link")
	require.Error(t, err)
	require.ErrorContains(t, repo.DeleteTaskRepository(ctx, "force-task-repository-delete-missing"), "task repository not found")
}
