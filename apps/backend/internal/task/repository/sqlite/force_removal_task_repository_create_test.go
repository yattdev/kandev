package sqlite

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

func TestClaimForceRemovalBlocksTaskRepositoryCreate(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-task-repository-create-ws", Name: "Force"}))
	for _, taskID := range []string{"force-task-repository-create-held", "force-task-repository-create-foreign"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: "force-task-repository-create-ws", Title: taskID}))
		repositoryID := taskID + "-repository"
		require.NoError(t, repo.CreateRepository(ctx, &models.Repository{ID: repositoryID, WorkspaceID: "force-task-repository-create-ws", Name: repositoryID, SourceType: "local", LocalPath: "/tmp/" + repositoryID, DefaultBranch: "main"}))
	}
	heldTask, err := repo.GetTask(ctx, "force-task-repository-create-held")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: heldTask.ID, WorkspaceID: heldTask.WorkspaceID, TaskGeneration: heldTask.UpdatedAt, AdmissionGeneration: "admission", OperationID: "task-repository-create", RequestDigest: "request", PreviewDigest: "preview"})
	require.NoError(t, err)

	heldLink := &models.TaskRepository{ID: "force-task-repository-create-held-link", TaskID: heldTask.ID, RepositoryID: "force-task-repository-create-held-repository", BaseBranch: "main"}
	require.ErrorIs(t, repo.CreateTaskRepository(ctx, heldLink), ErrForceRemovalTaskHeld)
	_, err = repo.GetTaskRepository(ctx, heldLink.ID)
	require.Error(t, err)

	foreignLink := &models.TaskRepository{ID: "force-task-repository-create-foreign-link", TaskID: "force-task-repository-create-foreign", RepositoryID: "force-task-repository-create-foreign-repository", BaseBranch: "main"}
	require.NoError(t, repo.CreateTaskRepository(ctx, foreignLink))
	stored, err := repo.GetTaskRepository(ctx, foreignLink.ID)
	require.NoError(t, err)
	require.Equal(t, foreignLink.TaskID, stored.TaskID)
}
