package sqlite

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

func TestClaimForceRemovalBlocksKubernetesEnvironmentCleanup(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForEntityTests(t)
	const (
		heldTaskID    = "force-kubernetes-cleanup-held"
		heldEnvID     = "force-kubernetes-cleanup-held-environment"
		foreignTaskID = "force-kubernetes-cleanup-foreign"
		foreignEnvID  = "force-kubernetes-cleanup-foreign-environment"
	)
	for taskID, environmentID := range map[string]string{
		heldTaskID: heldEnvID, foreignTaskID: foreignEnvID,
	} {
		seedRecoveryClaimEnvironment(t, repo, taskID, environmentID)
		record, err := repo.ClaimKubernetesEnvironment(ctx, environmentID, taskID, 1, "seed")
		require.NoError(t, err)
		require.NoError(t, repo.SaveKubernetesEnvironment(ctx, record, true))
	}

	heldTask, err := repo.GetTask(ctx, heldTaskID)
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{
		TaskID: heldTask.ID, WorkspaceID: heldTask.WorkspaceID, TaskGeneration: heldTask.UpdatedAt,
		AdmissionGeneration: "admission", OperationID: "kubernetes-cleanup", RequestDigest: "request", PreviewDigest: "preview",
	})
	require.NoError(t, err)

	_, err = repo.ClaimKubernetesEnvironmentCleanup(ctx, heldEnvID, heldTaskID, 1, "held")
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	held, err := repo.GetKubernetesEnvironment(ctx, heldEnvID)
	require.NoError(t, err)
	require.Empty(t, held.OperationID)

	foreign, err := repo.ClaimKubernetesEnvironmentCleanup(ctx, foreignEnvID, foreignTaskID, 1, "foreign")
	require.NoError(t, err)
	require.Equal(t, "cleanup:foreign", foreign.OperationID)

	_, err = repo.ClaimKubernetesEnvironmentCleanup(ctx, heldEnvID, heldTaskID, 2, "stale")
	require.ErrorIs(t, err, models.ErrKubernetesEnvironmentConflict)
}
