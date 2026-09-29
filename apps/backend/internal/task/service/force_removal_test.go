package service

import (
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository/sqlite"
	"github.com/stretchr/testify/require"
)

func TestAdmitForceRemovalClaimsExactTaskAndPreservesReceipt(t *testing.T) {
	ctx := ctxSynthetic()
	svc, _, repo := createTestService(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-service-ws", Name: "Force"}))
	for _, id := range []string{"held", "foreign", "stale"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: id, WorkspaceID: "force-service-ws", Title: id}))
	}
	request := func(id, op string) ForceRemovalAdmissionRequest {
		task, err := repo.GetTask(ctx, id)
		require.NoError(t, err)
		return ForceRemovalAdmissionRequest{TaskID: id, WorkspaceID: task.WorkspaceID, ExpectedTaskGeneration: exactRetirementGeneration(task), AdmissionGeneration: "admission", OperationID: op, RequestDigest: "request", PreviewDigest: "preview"}
	}

	claim, replay, err := svc.AdmitForceRemoval(ctx, request("held", "held-op"))
	require.NoError(t, err)
	require.False(t, replay)
	require.Equal(t, "held", claim.TaskID)
	receipts, err := repo.ListForceRemovalReceipts(ctx, "held-op")
	require.NoError(t, err)
	require.Len(t, receipts, 1)
	_, _, err = svc.AdmitForceRemoval(ctx, request("held", "other-op"))
	require.ErrorIs(t, err, sqlite.ErrForceRemovalClaimConflict)
	receipts, err = repo.ListForceRemovalReceipts(ctx, "held-op")
	require.NoError(t, err)
	require.Len(t, receipts, 1)

	claim, replay, err = svc.AdmitForceRemoval(ctx, request("foreign", "foreign-op"))
	require.NoError(t, err)
	require.False(t, replay)
	require.Equal(t, "foreign", claim.TaskID)
	stale := request("stale", "stale-op")
	stale.ExpectedTaskGeneration = "stale"
	_, _, err = svc.AdmitForceRemoval(ctx, stale)
	require.ErrorIs(t, err, ErrForceRemovalAdmissionStale)
}
