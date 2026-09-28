package sqlite

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

func TestClaimForceRemovalBlocksEnvironmentTransferToHeldDestination(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-transfer-ws", Name: "Force"}))
	for _, taskID := range []string{"force-transfer-source", "force-transfer-held-destination", "force-transfer-foreign-source", "force-transfer-foreign-destination"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: "force-transfer-ws", Title: taskID}))
	}
	require.NoError(t, repo.CreateTaskEnvironment(ctx, &models.TaskEnvironment{ID: "force-transfer-held-environment", TaskID: "force-transfer-source", ExecutorType: string(models.ExecutorTypeLocal), Status: models.TaskEnvironmentStatusCreating}))
	require.NoError(t, repo.CreateTaskEnvironment(ctx, &models.TaskEnvironment{ID: "force-transfer-foreign-environment", TaskID: "force-transfer-foreign-source", ExecutorType: string(models.ExecutorTypeLocal), Status: models.TaskEnvironmentStatusCreating}))

	heldDestination, err := repo.GetTask(ctx, "force-transfer-held-destination")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{
		TaskID: heldDestination.ID, WorkspaceID: heldDestination.WorkspaceID, TaskGeneration: heldDestination.UpdatedAt,
		AdmissionGeneration: "admission", OperationID: "transfer", RequestDigest: "request", PreviewDigest: "preview",
	})
	require.NoError(t, err)

	err = repo.TransferTaskEnvironmentOwnership(ctx, "force-transfer-held-environment", "force-transfer-source", 1, "force-transfer-held-destination")
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	heldEnvironment, err := repo.GetTaskEnvironment(ctx, "force-transfer-held-environment")
	require.NoError(t, err)
	require.Equal(t, "force-transfer-source", heldEnvironment.TaskID)
	require.EqualValues(t, 1, heldEnvironment.OwnershipGeneration)

	require.NoError(t, repo.TransferTaskEnvironmentOwnership(ctx, "force-transfer-foreign-environment", "force-transfer-foreign-source", 1, "force-transfer-foreign-destination"))
	foreignEnvironment, err := repo.GetTaskEnvironment(ctx, "force-transfer-foreign-environment")
	require.NoError(t, err)
	require.Equal(t, "force-transfer-foreign-destination", foreignEnvironment.TaskID)
	require.EqualValues(t, 2, foreignEnvironment.OwnershipGeneration)

	err = repo.TransferTaskEnvironmentOwnership(ctx, "force-transfer-foreign-environment", "force-transfer-foreign-source", 1, "force-transfer-foreign-destination")
	require.ErrorIs(t, err, ErrTaskEnvironmentOwnershipChanged)
}

func TestTransferTaskEnvironmentOwnershipOpposingTransfersComplete(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "opposing-transfer-ws", Name: "Opposing"}))
	for _, taskID := range []string{"opposing-transfer-a", "opposing-transfer-b"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: "opposing-transfer-ws", Title: taskID}))
	}
	require.NoError(t, repo.CreateTaskEnvironment(ctx, &models.TaskEnvironment{ID: "opposing-transfer-environment-a", TaskID: "opposing-transfer-a", ExecutorType: string(models.ExecutorTypeLocal), Status: models.TaskEnvironmentStatusCreating}))
	require.NoError(t, repo.CreateTaskEnvironment(ctx, &models.TaskEnvironment{ID: "opposing-transfer-environment-b", TaskID: "opposing-transfer-b", ExecutorType: string(models.ExecutorTypeLocal), Status: models.TaskEnvironmentStatusCreating}))

	results := make(chan error, 2)
	var start sync.WaitGroup
	start.Add(1)
	for _, transfer := range []struct{ environmentID, source, destination string }{
		{"opposing-transfer-environment-a", "opposing-transfer-a", "opposing-transfer-b"},
		{"opposing-transfer-environment-b", "opposing-transfer-b", "opposing-transfer-a"},
	} {
		transfer := transfer
		go func() {
			start.Wait()
			results <- repo.TransferTaskEnvironmentOwnership(ctx, transfer.environmentID, transfer.source, 1, transfer.destination)
		}()
	}
	start.Done()
	for range 2 {
		err := <-results
		require.Error(t, err)
		require.ErrorContains(t, err, "UNIQUE constraint")
	}
}
