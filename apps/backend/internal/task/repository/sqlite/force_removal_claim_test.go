package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kandev/kandev/internal/task/models"
)

// @covers AC-TASKS-SAFE-FORCE-REMOVAL-004.1
// @covers AC-TASKS-SAFE-FORCE-REMOVAL-004.2
func TestClaimForceRemovalRejectsStaleOrForeignTaskAndHoldsCleanup(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-ws", Name: "Force"}))
	require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: "force-task", WorkspaceID: "force-ws", Title: "Force"}))
	task, err := repo.GetTask(ctx, "force-task")
	require.NoError(t, err)

	claim := &models.ForceRemovalClaim{TaskID: task.ID, WorkspaceID: task.WorkspaceID, TaskGeneration: task.UpdatedAt, AdmissionGeneration: "admission", OperationID: "operation", RequestDigest: "request", PreviewDigest: "preview"}
	stored, replay, err := repo.ClaimForceRemoval(ctx, claim)
	require.NoError(t, err)
	require.False(t, replay)
	require.Equal(t, claim, stored)

	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: task.ID, WorkspaceID: "foreign", TaskGeneration: task.UpdatedAt, AdmissionGeneration: "admission", OperationID: "other", RequestDigest: "request", PreviewDigest: "preview"})
	require.ErrorIs(t, err, ErrForceRemovalClaimStale)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: task.ID, WorkspaceID: task.WorkspaceID, TaskGeneration: task.UpdatedAt, AdmissionGeneration: "admission", OperationID: "operation", RequestDigest: "different", PreviewDigest: "preview"})
	require.ErrorIs(t, err, ErrForceRemovalClaimConflict)

	err = repo.CreateTaskResourceCleanupJob(ctx, &models.TaskResourceCleanupJob{TaskID: task.ID, OperationID: "cleanup", Trigger: models.TaskResourceCleanupTriggerDelete, ResourceSnapshot: `{}`})
	require.ErrorIs(t, err, ErrForceRemovalCleanupHeld)
}
