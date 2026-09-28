package sqlite

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/orchestrator/messagequeue"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

func TestClaimForceRemovalBlocksDirectMessageAttachmentClaim(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForSessionTests(t)
	heldAttachment := seedPlanCommentMessageAttachment(t, ctx, repo, "force-direct-attachment-held", "held")
	foreignAttachment := seedPlanCommentMessageAttachment(t, ctx, repo, "force-direct-attachment-foreign", "foreign")
	staleAttachment := seedPlanCommentMessageAttachment(t, ctx, repo, "force-direct-attachment-stale", "stale")

	heldTask, err := repo.GetTask(ctx, "task-message-comments-force-direct-attachment-held")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{
		TaskID: heldTask.ID, WorkspaceID: heldTask.WorkspaceID, TaskGeneration: heldTask.UpdatedAt,
		AdmissionGeneration: "admission", OperationID: "direct-attachment-claim", RequestDigest: "request", PreviewDigest: "preview",
	})
	require.NoError(t, err)

	heldMessage := planCommentMessage("force-direct-attachment-held", "force-direct-attachment-held-message")
	_, err = requirePlanCommentMessageRepository(t, repo).CreateMessageWithPlanComments(
		ctx, heldMessage, []models.TaskPlanCommentRef{{ID: "comment-force-direct-attachment-held", Version: 1}}, true, "",
		&messagequeue.QueueAttachmentClaim{OwnerID: heldAttachment.OwnerID, WorkspaceID: heldAttachment.WorkspaceID, IDs: []string{heldAttachment.ID}},
	)
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	stored, err := repo.GetMessageAttachment(ctx, heldAttachment.ID)
	require.NoError(t, err)
	require.Equal(t, models.AttachmentStateStaged, stored.State)
	require.Empty(t, stored.TaskID)
	_, err = repo.GetMessageWithPromptIndex(ctx, heldMessage.ID)
	require.Error(t, err)

	foreignMessage := planCommentMessage("force-direct-attachment-foreign", "force-direct-attachment-foreign-message")
	_, err = requirePlanCommentMessageRepository(t, repo).CreateMessageWithPlanComments(
		ctx, foreignMessage, []models.TaskPlanCommentRef{{ID: "comment-force-direct-attachment-foreign", Version: 1}}, true, "",
		&messagequeue.QueueAttachmentClaim{OwnerID: foreignAttachment.OwnerID, WorkspaceID: foreignAttachment.WorkspaceID, IDs: []string{foreignAttachment.ID}},
	)
	require.NoError(t, err)
	stored, err = repo.GetMessageAttachment(ctx, foreignAttachment.ID)
	require.NoError(t, err)
	require.Equal(t, models.AttachmentStateClaimed, stored.State)
	require.Equal(t, foreignMessage.ID, stored.MessageID)

	staleMessage := planCommentMessage("force-direct-attachment-stale", "force-direct-attachment-stale-message")
	_, err = requirePlanCommentMessageRepository(t, repo).CreateMessageWithPlanComments(
		ctx, staleMessage, []models.TaskPlanCommentRef{{ID: "comment-force-direct-attachment-stale", Version: 9}}, false, "",
		&messagequeue.QueueAttachmentClaim{OwnerID: staleAttachment.OwnerID, WorkspaceID: staleAttachment.WorkspaceID, IDs: []string{staleAttachment.ID}},
	)
	require.Error(t, err)
	stored, err = repo.GetMessageAttachment(ctx, staleAttachment.ID)
	require.NoError(t, err)
	require.Equal(t, models.AttachmentStateStaged, stored.State)
}
