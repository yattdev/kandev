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
		return ForceRemovalAdmissionRequest{TaskID: id, WorkspaceID: task.WorkspaceID, ExpectedTaskGeneration: exactRetirementGeneration(task), AdmissionGeneration: "admission", OperationID: op, RequestDigest: "request", PreviewDigest: forceRemovalPreviewDigest(task)}
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

func TestPreviewForceRemovalDerivesFreshDigestAndRejectsStaleInput(t *testing.T) {
	ctx := ctxSynthetic()
	svc, _, repo := createTestService(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-preview-ws", Name: "Force"}))
	require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: "preview-task", WorkspaceID: "force-preview-ws", Title: "Force"}))
	task, err := repo.GetTask(ctx, "preview-task")
	require.NoError(t, err)

	preview, err := svc.PreviewForceRemoval(ctx, task.ID, task.WorkspaceID, exactRetirementGeneration(task))
	require.NoError(t, err)
	require.Equal(t, forceRemovalPreviewDigest(task), preview.Digest)
	require.Len(t, preview.Receipts, 1)
	require.Equal(t, models.ExactRetirementReceiptUnknown, preview.Receipts[0].Status)
	request := ForceRemovalAdmissionRequest{
		TaskID: task.ID, WorkspaceID: task.WorkspaceID, ExpectedTaskGeneration: preview.Generation,
		AdmissionGeneration: "admission", OperationID: "preview-operation", RequestDigest: "request",
		PreviewDigest: "caller-provided",
	}
	_, _, err = svc.AdmitForceRemoval(ctx, request)
	require.ErrorIs(t, err, ErrForceRemovalPreviewInvalid)

	task.Title = "changed"
	require.NoError(t, repo.UpdateTask(ctx, task))
	request.PreviewDigest = preview.Digest
	_, _, err = svc.AdmitForceRemoval(ctx, request)
	require.ErrorIs(t, err, ErrForceRemovalAdmissionStale)

	task, err = repo.GetTask(ctx, task.ID)
	require.NoError(t, err)
	preview, err = svc.PreviewForceRemoval(ctx, task.ID, task.WorkspaceID, exactRetirementGeneration(task))
	require.NoError(t, err)
	request.ExpectedTaskGeneration = preview.Generation
	request.PreviewDigest = preview.Digest
	claim, replay, err := svc.AdmitForceRemoval(ctx, request)
	require.NoError(t, err)
	require.False(t, replay)
	require.Equal(t, task.ID, claim.TaskID)
	receipts, err := repo.ListForceRemovalReceipts(ctx, request.OperationID)
	require.NoError(t, err)
	require.Len(t, receipts, 1)

	_, err = svc.PreviewForceRemoval(ctx, task.ID, "foreign-workspace", exactRetirementGeneration(task))
	require.ErrorIs(t, err, ErrForceRemovalAdmissionStale)
	_, err = svc.PreviewForceRemoval(ctx, task.ID, task.WorkspaceID, "stale")
	require.ErrorIs(t, err, ErrForceRemovalAdmissionStale)
}
