package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

func TestClaimForceRemovalBlocksExpiredAttachmentBatchWithoutPartialMutation(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	const workspaceID = "force-attachment-expiry-workspace"
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: workspaceID, Name: "Force"}))
	for _, taskID := range []string{"force-attachment-expiry-foreign", "force-attachment-expiry-held"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: workspaceID, Title: taskID}))
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	foreign := forceRemovalAttachment("force-attachment-expiry-foreign", workspaceID, "force-attachment-expiry-foreign")
	foreign.ExpiresAt = now.Add(-time.Minute)
	unbound := forceRemovalAttachment("force-attachment-expiry-unbound", workspaceID, "")
	unbound.ExpiresAt = now.Add(-time.Minute)
	held := forceRemovalAttachment("force-attachment-expiry-held", workspaceID, "force-attachment-expiry-held")
	held.ExpiresAt = now.Add(-time.Minute)
	for _, attachment := range []*models.TaskMessageAttachment{foreign, unbound, held} {
		require.NoError(t, repo.CreateMessageAttachment(ctx, attachment))
	}

	heldTask, err := repo.GetTask(ctx, held.TaskID)
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{
		TaskID: heldTask.ID, WorkspaceID: heldTask.WorkspaceID, TaskGeneration: heldTask.UpdatedAt,
		AdmissionGeneration: "admission", OperationID: "attachment-expiry", RequestDigest: "request", PreviewDigest: "preview",
	})
	require.NoError(t, err)

	_, err = repo.MarkExpiredMessageAttachments(ctx, now)
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	for _, attachmentID := range []string{foreign.ID, unbound.ID, held.ID} {
		stored, getErr := repo.GetMessageAttachment(ctx, attachmentID)
		require.NoError(t, getErr)
		require.Equal(t, models.AttachmentStateStaged, stored.State)
	}
}

func TestExpiredAttachmentBatchAllowsForeignAndUnboundOwners(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	const workspaceID = "force-attachment-expiry-progress-workspace"
	const taskID = "force-attachment-expiry-progress-task"
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: workspaceID, Name: "Force"}))
	require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: workspaceID, Title: "Foreign"}))

	now := time.Now().UTC().Truncate(time.Microsecond)
	foreign := forceRemovalAttachment("force-attachment-expiry-progress-foreign", workspaceID, taskID)
	foreign.ExpiresAt = now.Add(-time.Minute)
	unbound := forceRemovalAttachment("force-attachment-expiry-progress-unbound", workspaceID, "")
	unbound.ExpiresAt = now.Add(-time.Minute)
	for _, attachment := range []*models.TaskMessageAttachment{foreign, unbound} {
		require.NoError(t, repo.CreateMessageAttachment(ctx, attachment))
	}

	transitioned, err := repo.MarkExpiredMessageAttachments(ctx, now)
	require.NoError(t, err)
	require.Len(t, transitioned, 2)
	for _, attachmentID := range []string{foreign.ID, unbound.ID} {
		stored, getErr := repo.GetMessageAttachment(ctx, attachmentID)
		require.NoError(t, getErr)
		require.Equal(t, models.AttachmentStateExpired, stored.State)
	}
}

func TestExpiredAttachmentOwnerOrderIsStableAcrossCandidateOrders(t *testing.T) {
	forward := []*models.TaskMessageAttachment{
		{TaskID: "task-z"}, {TaskID: ""}, {TaskID: "task-a"}, {TaskID: "task-m"}, {TaskID: "task-a"},
	}
	reverse := []*models.TaskMessageAttachment{
		{TaskID: "task-a"}, {TaskID: "task-m"}, {TaskID: "task-a"}, {TaskID: ""}, {TaskID: "task-z"},
	}

	require.Equal(t, []string{"task-a", "task-m", "task-z"}, orderedExpiredAttachmentTaskIDs(forward))
	require.Equal(t, orderedExpiredAttachmentTaskIDs(forward), orderedExpiredAttachmentTaskIDs(reverse))
}
