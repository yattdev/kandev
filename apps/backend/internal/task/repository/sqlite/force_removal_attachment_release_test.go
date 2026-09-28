package sqlite

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

func TestClaimForceRemovalBlocksClaimedAttachmentReleasePreparation(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	const workspaceID = "force-attachment-release-workspace"
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: workspaceID, Name: "Force"}))
	for _, taskID := range []string{"force-attachment-release-held", "force-attachment-release-foreign"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: workspaceID, Title: taskID}))
	}
	heldAttachment := forceRemovalClaimedQueuedAttachment("force-attachment-release-held", workspaceID, "force-attachment-release-held", "")
	foreignAttachment := forceRemovalClaimedQueuedAttachment("force-attachment-release-foreign", workspaceID, "force-attachment-release-foreign", "")
	staleAttachment := forceRemovalClaimedQueuedAttachment("force-attachment-release-stale", workspaceID, "force-attachment-release-foreign", "")
	staleAttachment.OwnerID = "other-owner"
	for _, attachment := range []*models.TaskMessageAttachment{heldAttachment, foreignAttachment, staleAttachment} {
		require.NoError(t, repo.CreateMessageAttachment(ctx, attachment))
	}
	heldTask, err := repo.GetTask(ctx, "force-attachment-release-held")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: heldTask.ID, WorkspaceID: heldTask.WorkspaceID, TaskGeneration: heldTask.UpdatedAt, AdmissionGeneration: "admission", OperationID: "attachment-release", RequestDigest: "request", PreviewDigest: "preview"})
	require.NoError(t, err)

	_, err = repo.PrepareClaimedMessageAttachmentsForRelease(ctx, []string{heldAttachment.ID}, "owner", heldTask.ID, "session")
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	stored, err := repo.GetMessageAttachment(ctx, heldAttachment.ID)
	require.NoError(t, err)
	require.Equal(t, models.AttachmentStateClaimed, stored.State)

	released, err := repo.PrepareClaimedMessageAttachmentsForRelease(ctx, []string{foreignAttachment.ID}, "owner", "force-attachment-release-foreign", "session")
	require.NoError(t, err)
	require.Len(t, released, 1)
	stored, err = repo.GetMessageAttachment(ctx, foreignAttachment.ID)
	require.NoError(t, err)
	require.Equal(t, models.AttachmentStateExpired, stored.State)

	released, err = repo.PrepareClaimedMessageAttachmentsForRelease(ctx, []string{staleAttachment.ID}, "owner", "force-attachment-release-foreign", "session")
	require.NoError(t, err)
	require.Empty(t, released)
	stored, err = repo.GetMessageAttachment(ctx, staleAttachment.ID)
	require.NoError(t, err)
	require.Equal(t, models.AttachmentStateClaimed, stored.State)
}
