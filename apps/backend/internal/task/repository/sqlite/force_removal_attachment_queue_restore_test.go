package sqlite

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

func TestClaimForceRemovalBlocksQueuedMessageAttachmentRestore(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	const workspaceID = "force-attachment-queue-restore-workspace"
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: workspaceID, Name: "Force"}))
	for _, taskID := range []string{"force-attachment-queue-restore-held", "force-attachment-queue-restore-foreign"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: workspaceID, Title: taskID}))
	}

	heldAttachment := forceRemovalClaimedQueuedAttachment("force-attachment-queue-restore-held", workspaceID, "force-attachment-queue-restore-held", "queue-held")
	foreignAttachment := forceRemovalClaimedQueuedAttachment("force-attachment-queue-restore-foreign", workspaceID, "force-attachment-queue-restore-foreign", "queue-foreign")
	staleAttachment := forceRemovalClaimedQueuedAttachment("force-attachment-queue-restore-stale", workspaceID, "force-attachment-queue-restore-foreign", "queue-other")
	for _, attachment := range []*models.TaskMessageAttachment{heldAttachment, foreignAttachment, staleAttachment} {
		require.NoError(t, repo.CreateMessageAttachment(ctx, attachment))
	}

	heldTask, err := repo.GetTask(ctx, "force-attachment-queue-restore-held")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{
		TaskID: heldTask.ID, WorkspaceID: heldTask.WorkspaceID, TaskGeneration: heldTask.UpdatedAt,
		AdmissionGeneration: "admission", OperationID: "queued-attachment-restore", RequestDigest: "request", PreviewDigest: "preview",
	})
	require.NoError(t, err)

	require.ErrorIs(t, repo.RestoreQueuedMessageAttachments(ctx, []string{heldAttachment.ID}, "owner", heldTask.ID, "session", "queue-held"), ErrForceRemovalTaskHeld)
	stored, err := repo.GetMessageAttachment(ctx, heldAttachment.ID)
	require.NoError(t, err)
	require.Equal(t, models.AttachmentStateClaimed, stored.State)
	require.Equal(t, heldTask.ID, stored.TaskID)
	require.Equal(t, "queue-held", stored.QueueID)

	require.NoError(t, repo.RestoreQueuedMessageAttachments(ctx, []string{foreignAttachment.ID}, "owner", "force-attachment-queue-restore-foreign", "session", "queue-foreign"))
	stored, err = repo.GetMessageAttachment(ctx, foreignAttachment.ID)
	require.NoError(t, err)
	require.Equal(t, models.AttachmentStateStaged, stored.State)
	require.Empty(t, stored.TaskID)
	require.Empty(t, stored.QueueID)

	require.NoError(t, repo.RestoreQueuedMessageAttachments(ctx, []string{staleAttachment.ID}, "owner", "force-attachment-queue-restore-foreign", "session", "queue-stale"))
	stored, err = repo.GetMessageAttachment(ctx, staleAttachment.ID)
	require.NoError(t, err)
	require.Equal(t, models.AttachmentStateClaimed, stored.State)
	require.Equal(t, "queue-other", stored.QueueID)
}

func forceRemovalClaimedQueuedAttachment(id, workspaceID, taskID, queueID string) *models.TaskMessageAttachment {
	attachment := forceRemovalAttachment(id, workspaceID, taskID)
	attachment.SessionID = "session"
	attachment.QueueID = queueID
	attachment.State = models.AttachmentStateClaimed
	return attachment
}
