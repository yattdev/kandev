package sqlite

import (
	"context"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestClaimForceRemovalBlocksTaskAttachmentDeletePreparation(t *testing.T) {
	ctx := context.Background()
	r := newRepoForHealTests(t)
	const ws = "force-prepare-attachment-delete-ws"
	require.NoError(t, r.CreateWorkspace(ctx, &models.Workspace{ID: ws, Name: "Force"}))
	for _, id := range []string{"force-prepare-attachment-held", "force-prepare-attachment-foreign"} {
		require.NoError(t, r.CreateTask(ctx, &models.Task{ID: id, WorkspaceID: ws, Title: id}))
	}
	h := forceRemovalAttachment("force-prepare-attachment-held-row", ws, "force-prepare-attachment-held")
	f := forceRemovalAttachment("force-prepare-attachment-foreign-row", ws, "force-prepare-attachment-foreign")
	require.NoError(t, r.CreateMessageAttachment(ctx, h))
	require.NoError(t, r.CreateMessageAttachment(ctx, f))
	task, err := r.GetTask(ctx, h.TaskID)
	require.NoError(t, err)
	_, _, err = r.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: task.ID, WorkspaceID: task.WorkspaceID, TaskGeneration: task.UpdatedAt, AdmissionGeneration: "admission", OperationID: "prepare-attachment-delete", RequestDigest: "request", PreviewDigest: "preview"})
	require.NoError(t, err)
	_, err = r.PrepareMessageAttachmentsForTaskDelete(ctx, h.TaskID)
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	stored, err := r.GetMessageAttachment(ctx, h.ID)
	require.NoError(t, err)
	require.Equal(t, models.AttachmentStateStaged, stored.State)
	prepared, err := r.PrepareMessageAttachmentsForTaskDelete(ctx, f.TaskID)
	require.NoError(t, err)
	require.Len(t, prepared, 1)
	stored, err = r.GetMessageAttachment(ctx, f.ID)
	require.NoError(t, err)
	require.Equal(t, models.AttachmentStateExpired, stored.State)
}
