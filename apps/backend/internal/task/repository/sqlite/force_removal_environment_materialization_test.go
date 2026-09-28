package sqlite

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

func TestClaimForceRemovalBlocksEnvironmentMaterialization(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-materialization-ws", Name: "Force"}))

	createEnvironment := func(taskID, materializer string) *models.TaskEnvironment {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: "force-materialization-ws", Title: taskID}))
		environment := &models.TaskEnvironment{
			ID:                       taskID + "-environment",
			TaskID:                   taskID,
			ExecutorType:             string(models.ExecutorTypeLocal),
			Status:                   models.TaskEnvironmentStatusCreating,
			MaterializationSessionID: materializer,
		}
		require.NoError(t, repo.CreateTaskEnvironment(ctx, environment))
		return environment
	}

	held := createEnvironment("force-materialization-held", "held-materializer")
	foreign := createEnvironment("force-materialization-foreign", "foreign-materializer")
	stale := createEnvironment("force-materialization-stale", "current-materializer")

	heldTask, err := repo.GetTask(ctx, held.TaskID)
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{
		TaskID: heldTask.ID, WorkspaceID: heldTask.WorkspaceID, TaskGeneration: heldTask.UpdatedAt,
		AdmissionGeneration: "admission", OperationID: "materialize", RequestDigest: "request", PreviewDigest: "preview",
	})
	require.NoError(t, err)

	require.ErrorIs(t, repo.FinalizeTaskEnvironmentMaterialization(ctx, held, nil, "held-materializer"), ErrForceRemovalTaskHeld)
	heldStored, err := repo.GetTaskEnvironment(ctx, held.ID)
	require.NoError(t, err)
	require.Equal(t, models.TaskEnvironmentStatusCreating, heldStored.Status)
	require.Equal(t, "held-materializer", heldStored.MaterializationSessionID)
	require.Empty(t, heldStored.Repos)

	require.NoError(t, repo.FinalizeTaskEnvironmentMaterialization(ctx, foreign, nil, "foreign-materializer"))
	foreignStored, err := repo.GetTaskEnvironment(ctx, foreign.ID)
	require.NoError(t, err)
	require.Equal(t, models.TaskEnvironmentStatusReady, foreignStored.Status)
	require.Empty(t, foreignStored.MaterializationSessionID)

	err = repo.FinalizeTaskEnvironmentMaterialization(ctx, stale, nil, "superseded-materializer")
	require.ErrorContains(t, err, "materialization claim was not current")
	staleStored, err := repo.GetTaskEnvironment(ctx, stale.ID)
	require.NoError(t, err)
	require.Equal(t, models.TaskEnvironmentStatusCreating, staleStored.Status)
	require.Equal(t, "current-materializer", staleStored.MaterializationSessionID)
}
