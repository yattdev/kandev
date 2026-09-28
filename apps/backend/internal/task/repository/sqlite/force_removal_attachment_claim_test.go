package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

func TestClaimForceRemovalBlocksMessageAttachmentClaim(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	const workspaceID = "force-attachment-claim-workspace"
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: workspaceID, Name: "Force"}))
	for _, taskID := range []string{"force-attachment-claim-held", "force-attachment-claim-foreign"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: workspaceID, Title: taskID}))
	}

	heldTask, err := repo.GetTask(ctx, "force-attachment-claim-held")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{
		TaskID: heldTask.ID, WorkspaceID: heldTask.WorkspaceID, TaskGeneration: heldTask.UpdatedAt,
		AdmissionGeneration: "admission", OperationID: "attachment-claim", RequestDigest: "request", PreviewDigest: "preview",
	})
	require.NoError(t, err)

	heldAttachment := forceRemovalAttachment("force-attachment-claim-held", workspaceID, "")
	require.NoError(t, repo.CreateMessageAttachment(ctx, heldAttachment))
	require.ErrorIs(t, repo.ClaimMessageAttachments(ctx, []string{heldAttachment.ID}, "owner", workspaceID, heldTask.ID, ""), ErrForceRemovalTaskHeld)
	stored, err := repo.GetMessageAttachment(ctx, heldAttachment.ID)
	require.NoError(t, err)
	require.Equal(t, models.AttachmentStateStaged, stored.State)
	require.Empty(t, stored.TaskID)

	foreignAttachment := forceRemovalAttachment("force-attachment-claim-foreign", workspaceID, "")
	require.NoError(t, repo.CreateMessageAttachment(ctx, foreignAttachment))
	require.NoError(t, repo.ClaimMessageAttachments(ctx, []string{foreignAttachment.ID}, "owner", workspaceID, "force-attachment-claim-foreign", ""))
	stored, err = repo.GetMessageAttachment(ctx, foreignAttachment.ID)
	require.NoError(t, err)
	require.Equal(t, models.AttachmentStateClaimed, stored.State)
	require.Equal(t, "force-attachment-claim-foreign", stored.TaskID)

	expiredAttachment := forceRemovalAttachment("force-attachment-claim-expired", workspaceID, "")
	expiredAttachment.ExpiresAt = time.Now().Add(-time.Minute)
	require.NoError(t, repo.CreateMessageAttachment(ctx, expiredAttachment))
	require.ErrorIs(t, repo.ClaimMessageAttachments(ctx, []string{expiredAttachment.ID}, "owner", workspaceID, "force-attachment-claim-foreign", ""), models.ErrAttachmentClaimConflict)
	stored, err = repo.GetMessageAttachment(ctx, expiredAttachment.ID)
	require.NoError(t, err)
	require.Equal(t, models.AttachmentStateStaged, stored.State)
}
