package sqlite

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

func TestClaimForceRemovalBlocksTaskScopedClaimedAttachmentDelete(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	const workspaceID = "force-claimed-delete-workspace"
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: workspaceID, Name: "Force"}))
	for _, id := range []string{"force-claimed-delete-held", "force-claimed-delete-foreign"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: id, WorkspaceID: workspaceID, Title: id}))
	}
	held := forceRemovalClaimedQueuedAttachment("force-claimed-delete-held-attachment", workspaceID, "force-claimed-delete-held", "")
	foreign := forceRemovalClaimedQueuedAttachment("force-claimed-delete-foreign-attachment", workspaceID, "force-claimed-delete-foreign", "")
	held.SessionID, foreign.SessionID = "", ""
	require.NoError(t, repo.CreateMessageAttachment(ctx, held))
	require.NoError(t, repo.CreateMessageAttachment(ctx, foreign))
	task, err := repo.GetTask(ctx, "force-claimed-delete-held")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: task.ID, WorkspaceID: task.WorkspaceID, TaskGeneration: task.UpdatedAt, AdmissionGeneration: "admission", OperationID: "claimed-delete", RequestDigest: "request", PreviewDigest: "preview"})
	require.NoError(t, err)
	_, err = repo.DeleteClaimedMessageAttachmentsByTaskSession(ctx, []string{held.ID}, task.ID, "")
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	_, err = repo.GetMessageAttachment(ctx, held.ID)
	require.NoError(t, err)
	removed, err := repo.DeleteClaimedMessageAttachmentsByTaskSession(ctx, []string{foreign.ID}, "force-claimed-delete-foreign", "")
	require.NoError(t, err)
	require.Len(t, removed, 1)
}
