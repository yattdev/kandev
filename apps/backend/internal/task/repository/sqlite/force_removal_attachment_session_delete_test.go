package sqlite

import (
	"context"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestClaimForceRemovalBlocksSessionAttachmentDelete(t *testing.T) {
	ctx := context.Background()
	r := newRepoForHealTests(t)
	const ws = "force-session-attachment-delete-ws"
	require.NoError(t, r.CreateWorkspace(ctx, &models.Workspace{ID: ws, Name: "Force"}))
	for _, id := range []string{"force-session-attachment-held", "force-session-attachment-foreign"} {
		require.NoError(t, r.CreateTask(ctx, &models.Task{ID: id, WorkspaceID: ws, Title: id}))
	}
	h := forceRemovalAttachment("force-session-attachment-held-row", ws, "force-session-attachment-held")
	f := forceRemovalAttachment("force-session-attachment-foreign-row", ws, "force-session-attachment-foreign")
	h.SessionID = "s"
	f.SessionID = "s"
	require.NoError(t, r.CreateMessageAttachment(ctx, h))
	require.NoError(t, r.CreateMessageAttachment(ctx, f))
	task, err := r.GetTask(ctx, h.TaskID)
	require.NoError(t, err)
	_, _, err = r.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: task.ID, WorkspaceID: task.WorkspaceID, TaskGeneration: task.UpdatedAt, AdmissionGeneration: "admission", OperationID: "session-attachment-delete", RequestDigest: "request", PreviewDigest: "preview"})
	require.NoError(t, err)
	_, err = r.DeleteMessageAttachmentsBySession(ctx, h.TaskID, "s")
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	_, err = r.GetMessageAttachment(ctx, h.ID)
	require.NoError(t, err)
	removed, err := r.DeleteMessageAttachmentsBySession(ctx, f.TaskID, "s")
	require.NoError(t, err)
	require.Len(t, removed, 1)
	removed, err = r.DeleteMessageAttachmentsBySession(ctx, "", "s")
	require.NoError(t, err)
	require.Empty(t, removed)
	removed, err = r.DeleteMessageAttachmentsBySession(ctx, f.TaskID, "")
	require.NoError(t, err)
	require.Empty(t, removed)
}
