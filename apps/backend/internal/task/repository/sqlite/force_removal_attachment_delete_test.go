package sqlite

import (
	"context"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestClaimForceRemovalBlocksMessageAttachmentDelete(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	const ws = "force-attachment-delete-ws"
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: ws, Name: "Force"}))
	for _, id := range []string{"force-attachment-delete-held", "force-attachment-delete-foreign"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: id, WorkspaceID: ws, Title: id}))
	}
	held := forceRemovalAttachment("force-attachment-delete-held-row", ws, "force-attachment-delete-held")
	foreign := forceRemovalAttachment("force-attachment-delete-foreign-row", ws, "force-attachment-delete-foreign")
	staged := forceRemovalAttachment("force-attachment-delete-staged-row", ws, "")
	for _, a := range []*models.TaskMessageAttachment{held, foreign, staged} {
		require.NoError(t, repo.CreateMessageAttachment(ctx, a))
	}
	task, err := repo.GetTask(ctx, "force-attachment-delete-held")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: task.ID, WorkspaceID: task.WorkspaceID, TaskGeneration: task.UpdatedAt, AdmissionGeneration: "admission", OperationID: "attachment-delete", RequestDigest: "request", PreviewDigest: "preview"})
	require.NoError(t, err)
	require.ErrorIs(t, repo.DeleteMessageAttachment(ctx, held.ID, "owner"), ErrForceRemovalTaskHeld)
	_, err = repo.GetMessageAttachment(ctx, held.ID)
	require.NoError(t, err)
	require.NoError(t, repo.DeleteMessageAttachment(ctx, foreign.ID, "owner"))
	_, err = repo.GetMessageAttachment(ctx, foreign.ID)
	require.ErrorIs(t, err, models.ErrAttachmentNotFound)
	require.ErrorIs(t, repo.DeleteMessageAttachment(ctx, staged.ID, "other"), models.ErrAttachmentNotFound)
	_, err = repo.GetMessageAttachment(ctx, staged.ID)
	require.NoError(t, err)
}
