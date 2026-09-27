package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/testutil"
)

// @covers AC-TASKS-SAFE-FORCE-REMOVAL-004.1
// @covers AC-TASKS-SAFE-FORCE-REMOVAL-004.2
func TestPostgresForceRemovalClaimRejectsStaleAndReplaysExactRequest(t *testing.T) {
	db := testutil.OpenIsolatedPostgres(t, testutil.PostgresDSNFromEnv(t))
	repo, err := NewWithDB(db, db, nil)
	require.NoError(t, err)
	ctx := context.Background()
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-pg-ws", Name: "Force"}))
	require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: "force-pg-task", WorkspaceID: "force-pg-ws", Title: "Force"}))
	task, err := repo.GetTask(ctx, "force-pg-task")
	require.NoError(t, err)
	claim := &models.ForceRemovalClaim{TaskID: task.ID, WorkspaceID: task.WorkspaceID, TaskGeneration: task.UpdatedAt, AdmissionGeneration: "admission", OperationID: "operation", RequestDigest: "request", PreviewDigest: "preview"}
	_, replay, err := repo.ClaimForceRemoval(ctx, claim)
	require.NoError(t, err)
	require.False(t, replay)
	_, replay, err = repo.ClaimForceRemoval(ctx, claim)
	require.NoError(t, err)
	require.True(t, replay)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: task.ID, WorkspaceID: task.WorkspaceID, TaskGeneration: task.UpdatedAt, AdmissionGeneration: "admission", OperationID: "operation", RequestDigest: "changed", PreviewDigest: "preview"})
	require.ErrorIs(t, err, ErrForceRemovalClaimConflict)
	receipt := models.ExactRetirementPredicateReceipt{Predicate: models.ExactRetirementIdentityPredicate, Status: models.ExactRetirementReceiptPass, ReasonCode: "EXACT_TASK_CLAIMED", ResourceID: task.ID, ObservedGeneration: "generation", EvidenceDigest: "digest"}
	require.NoError(t, repo.AppendForceRemovalReceipt(ctx, claim.OperationID, receipt))
	require.NoError(t, repo.AppendForceRemovalReceipt(ctx, claim.OperationID, receipt))
	receipt.EvidenceDigest = "changed"
	require.ErrorIs(t, repo.AppendForceRemovalReceipt(ctx, claim.OperationID, receipt), ErrForceRemovalClaimConflict)
}
