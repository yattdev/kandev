package sqlite

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

func TestClaimForceRemovalBlocksTaskMessageAttachmentCreate(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	const workspaceID = "force-attachment-create-workspace"
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: workspaceID, Name: "Force"}))
	for _, taskID := range []string{"force-attachment-create-held", "force-attachment-create-foreign"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: workspaceID, Title: taskID}))
	}

	heldTask, err := repo.GetTask(ctx, "force-attachment-create-held")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{
		TaskID: heldTask.ID, WorkspaceID: heldTask.WorkspaceID, TaskGeneration: heldTask.UpdatedAt,
		AdmissionGeneration: "admission", OperationID: "attachment-create", RequestDigest: "request", PreviewDigest: "preview",
	})
	require.NoError(t, err)

	heldAttachment := forceRemovalAttachment("force-attachment-create-held", workspaceID, heldTask.ID)
	require.ErrorIs(t, repo.CreateMessageAttachment(ctx, heldAttachment), ErrForceRemovalTaskHeld)
	_, err = repo.GetMessageAttachment(ctx, heldAttachment.ID)
	require.ErrorIs(t, err, models.ErrAttachmentNotFound)

	foreignAttachment := forceRemovalAttachment("force-attachment-create-foreign", workspaceID, "force-attachment-create-foreign")
	require.NoError(t, repo.CreateMessageAttachment(ctx, foreignAttachment))
	stored, err := repo.GetMessageAttachment(ctx, foreignAttachment.ID)
	require.NoError(t, err)
	require.Equal(t, foreignAttachment.TaskID, stored.TaskID)

	stagedAttachment := forceRemovalAttachment("force-attachment-create-staged", workspaceID, "")
	require.NoError(t, repo.CreateMessageAttachment(ctx, stagedAttachment))
	stored, err = repo.GetMessageAttachment(ctx, stagedAttachment.ID)
	require.NoError(t, err)
	require.Empty(t, stored.TaskID)

	missingAttachment := forceRemovalAttachment("force-attachment-create-missing", workspaceID, "force-attachment-create-missing-task")
	require.NoError(t, repo.CreateMessageAttachment(ctx, missingAttachment))
	stored, err = repo.GetMessageAttachment(ctx, missingAttachment.ID)
	require.NoError(t, err)
	require.Equal(t, missingAttachment.TaskID, stored.TaskID)
}

func forceRemovalAttachment(id, workspaceID, taskID string) *models.TaskMessageAttachment {
	return &models.TaskMessageAttachment{
		ID: id, OwnerID: "owner", WorkspaceID: workspaceID, TaskID: taskID,
		Name: "notes.txt", MimeType: "text/plain", Kind: "resource", DeliveryMode: "path",
		SizeBytes: 1, StorageKey: id, State: models.AttachmentStateStaged,
	}
}
