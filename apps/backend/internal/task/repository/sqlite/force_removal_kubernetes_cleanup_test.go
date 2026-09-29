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

func TestClaimForceRemovalBlocksKubernetesEnvironmentAdmission(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForEntityTests(t)
	const (
		heldTaskID    = "force-kubernetes-admission-held"
		heldEnvID     = "force-kubernetes-admission-held-environment"
		foreignTaskID = "force-kubernetes-admission-foreign"
		foreignEnvID  = "force-kubernetes-admission-foreign-environment"
	)
	seedRecoveryClaimEnvironment(t, repo, heldTaskID, heldEnvID)
	seedRecoveryClaimEnvironment(t, repo, foreignTaskID, foreignEnvID)

	heldTask, err := repo.GetTask(ctx, heldTaskID)
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{
		TaskID: heldTask.ID, WorkspaceID: heldTask.WorkspaceID, TaskGeneration: heldTask.UpdatedAt,
		AdmissionGeneration: "admission", OperationID: "kubernetes-admission", RequestDigest: "request", PreviewDigest: "preview",
	})
	require.NoError(t, err)

	_, err = repo.ClaimKubernetesEnvironment(ctx, heldEnvID, heldTaskID, 1, "held")
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	_, err = repo.GetKubernetesEnvironment(ctx, heldEnvID)
	require.ErrorIs(t, err, models.ErrKubernetesEnvironmentNotFound)

	foreign, err := repo.ClaimKubernetesEnvironment(ctx, foreignEnvID, foreignTaskID, 1, "foreign")
	require.NoError(t, err)
	require.Equal(t, "foreign", foreign.OperationID)

	_, err = repo.ClaimKubernetesEnvironment(ctx, heldEnvID, heldTaskID, 2, "stale")
	require.ErrorIs(t, err, models.ErrKubernetesEnvironmentConflict)
}

func TestClaimForceRemovalBlocksKubernetesEnvironmentCheckpoint(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForEntityTests(t)
	const (
		heldTaskID    = "force-kubernetes-checkpoint-held"
		heldEnvID     = "force-kubernetes-checkpoint-held-environment"
		foreignTaskID = "force-kubernetes-checkpoint-foreign"
		foreignEnvID  = "force-kubernetes-checkpoint-foreign-environment"
		deletedTaskID = "force-kubernetes-checkpoint-deleted"
		deletedEnvID  = "force-kubernetes-checkpoint-deleted-environment"
	)
	for taskID, environmentID := range map[string]string{
		heldTaskID: heldEnvID, foreignTaskID: foreignEnvID, deletedTaskID: deletedEnvID,
	} {
		seedRecoveryClaimEnvironment(t, repo, taskID, environmentID)
	}
	held, err := repo.ClaimKubernetesEnvironment(ctx, heldEnvID, heldTaskID, 1, "held")
	require.NoError(t, err)
	foreign, err := repo.ClaimKubernetesEnvironment(ctx, foreignEnvID, foreignTaskID, 1, "foreign")
	require.NoError(t, err)
	staleForeign := *foreign
	deleted, err := repo.ClaimKubernetesEnvironment(ctx, deletedEnvID, deletedTaskID, 1, "deleted")
	require.NoError(t, err)

	heldTask, err := repo.GetTask(ctx, heldTaskID)
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{
		TaskID: heldTask.ID, WorkspaceID: heldTask.WorkspaceID, TaskGeneration: heldTask.UpdatedAt,
		AdmissionGeneration: "admission", OperationID: "kubernetes-checkpoint", RequestDigest: "request", PreviewDigest: "preview",
	})
	require.NoError(t, err)

	held.Metadata = map[string]interface{}{"checkpoint": "blocked"}
	require.ErrorIs(t, repo.SaveKubernetesEnvironment(ctx, held, true), ErrForceRemovalTaskHeld)
	storedHeld, err := repo.GetKubernetesEnvironment(ctx, heldEnvID)
	require.NoError(t, err)
	require.Equal(t, "held", storedHeld.OperationID)
	require.Empty(t, storedHeld.Metadata)
	require.Equal(t, int64(2), storedHeld.Revision)

	foreign.Metadata = map[string]interface{}{"checkpoint": "foreign"}
	require.NoError(t, repo.SaveKubernetesEnvironment(ctx, foreign, false))
	require.ErrorIs(t, repo.SaveKubernetesEnvironment(ctx, &staleForeign, true), models.ErrKubernetesEnvironmentConflict)

	require.NoError(t, repo.SaveKubernetesEnvironment(ctx, deleted, true))
	require.NoError(t, repo.DeleteTask(ctx, deletedTaskID))
	deleted, err = repo.ClaimKubernetesEnvironmentCleanup(ctx, deletedEnvID, deletedTaskID, 1, "deleted-cleanup")
	require.NoError(t, err)
	deleted.Metadata = map[string]interface{}{"checkpoint": "durable-cleanup"}
	require.NoError(t, repo.SaveKubernetesEnvironment(ctx, deleted, true))
	storedDeleted, err := repo.GetKubernetesEnvironment(ctx, deletedEnvID)
	require.NoError(t, err)
	require.Empty(t, storedDeleted.OperationID)
	require.Equal(t, "durable-cleanup", storedDeleted.Metadata["checkpoint"])
}
