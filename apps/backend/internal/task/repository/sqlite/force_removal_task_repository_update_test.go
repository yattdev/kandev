package sqlite

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

func TestClaimForceRemovalBlocksTaskRepositoryUpdate(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-task-repository-update-ws", Name: "Force"}))
	for _, taskID := range []string{"force-task-repository-update-held", "force-task-repository-update-foreign"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: "force-task-repository-update-ws", Title: taskID}))
	}
	for _, repositoryID := range []string{"repository-held", "repository-foreign"} {
		require.NoError(t, repo.CreateRepository(ctx, &models.Repository{ID: repositoryID, WorkspaceID: "force-task-repository-update-ws", Name: repositoryID, SourceType: "local", LocalPath: "/tmp/" + repositoryID, DefaultBranch: "main"}))
	}
	heldLink := &models.TaskRepository{ID: "force-task-repository-update-held-link", TaskID: "force-task-repository-update-held", RepositoryID: "repository-held", BaseBranch: "main"}
	foreignLink := &models.TaskRepository{ID: "force-task-repository-update-foreign-link", TaskID: "force-task-repository-update-foreign", RepositoryID: "repository-foreign", BaseBranch: "main"}
	require.NoError(t, repo.CreateTaskRepository(ctx, heldLink))
	require.NoError(t, repo.CreateTaskRepository(ctx, foreignLink))

	heldTask, err := repo.GetTask(ctx, heldLink.TaskID)
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: heldTask.ID, WorkspaceID: heldTask.WorkspaceID, TaskGeneration: heldTask.UpdatedAt, AdmissionGeneration: "admission", OperationID: "task-repository-update", RequestDigest: "request", PreviewDigest: "preview"})
	require.NoError(t, err)

	heldLink.BaseBranch = "held-after"
	require.ErrorIs(t, repo.UpdateTaskRepository(ctx, heldLink), ErrForceRemovalTaskHeld)
	heldStored, err := repo.GetTaskRepository(ctx, heldLink.ID)
	require.NoError(t, err)
	require.Equal(t, "main", heldStored.BaseBranch)

	foreignLink.BaseBranch = "foreign-after"
	require.NoError(t, repo.UpdateTaskRepository(ctx, foreignLink))
	foreignStored, err := repo.GetTaskRepository(ctx, foreignLink.ID)
	require.NoError(t, err)
	require.Equal(t, "foreign-after", foreignStored.BaseBranch)
	require.ErrorContains(t, repo.UpdateTaskRepository(ctx, &models.TaskRepository{ID: "force-task-repository-update-missing"}), "task repository not found")
}
