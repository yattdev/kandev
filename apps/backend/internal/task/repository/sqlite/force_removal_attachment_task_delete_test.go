package sqlite

import (
	"context"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestClaimForceRemovalBlocksTaskAttachmentDelete(t *testing.T) {
	ctx := context.Background()
	r := newRepoForHealTests(t)
	const ws = "force-task-attachment-delete-ws"
	require.NoError(t, r.CreateWorkspace(ctx, &models.Workspace{ID: ws, Name: "Force"}))
	for _, id := range []string{"force-task-attachment-delete-held", "force-task-attachment-delete-foreign"} {
		require.NoError(t, r.CreateTask(ctx, &models.Task{ID: id, WorkspaceID: ws, Title: id}))
	}
	h := forceRemovalAttachment("force-task-attachment-delete-held-row", ws, "force-task-attachment-delete-held")
	f := forceRemovalAttachment("force-task-attachment-delete-foreign-row", ws, "force-task-attachment-delete-foreign")
	require.NoError(t, r.CreateMessageAttachment(ctx, h))
	require.NoError(t, r.CreateMessageAttachment(ctx, f))
	task, err := r.GetTask(ctx, h.TaskID)
	require.NoError(t, err)
	_, _, err = r.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: task.ID, WorkspaceID: task.WorkspaceID, TaskGeneration: task.UpdatedAt, AdmissionGeneration: "admission", OperationID: "task-attachment-delete", RequestDigest: "request", PreviewDigest: "preview"})
	require.NoError(t, err)
	_, err = r.DeleteMessageAttachmentsByTask(ctx, h.TaskID)
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	_, err = r.GetMessageAttachment(ctx, h.ID)
	require.NoError(t, err)
	removed, err := r.DeleteMessageAttachmentsByTask(ctx, f.TaskID)
	require.NoError(t, err)
	require.Len(t, removed, 1)
	removed, err = r.DeleteMessageAttachmentsByTask(ctx, "")
	require.NoError(t, err)
	require.Empty(t, removed)
}
