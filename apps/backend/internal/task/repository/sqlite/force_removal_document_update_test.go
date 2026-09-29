package sqlite

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

func TestClaimForceRemovalBlocksDocumentUpdate(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	const workspaceID = "force-document-update-workspace"
	const heldTaskID = "force-document-update-held"
	const foreignTaskID = "force-document-update-foreign"
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: workspaceID, Name: "Force"}))
	for _, taskID := range []string{heldTaskID, foreignTaskID} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: workspaceID, Title: taskID}))
	}
	heldDoc := &models.TaskDocument{ID: "force-document-update-held", TaskID: heldTaskID, Key: "notes", Type: "note", Content: "before"}
	foreignDoc := &models.TaskDocument{ID: "force-document-update-foreign", TaskID: foreignTaskID, Key: "notes", Type: "note", Content: "before"}
	require.NoError(t, repo.CreateDocument(ctx, heldDoc))
	require.NoError(t, repo.CreateDocument(ctx, foreignDoc))

	heldTask, err := repo.GetTask(ctx, heldTaskID)
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: heldTask.ID, WorkspaceID: heldTask.WorkspaceID, TaskGeneration: heldTask.UpdatedAt, AdmissionGeneration: "admission", OperationID: "document-update", RequestDigest: "request", PreviewDigest: "preview"})
	require.NoError(t, err)

	heldDoc.Content = "must not persist"
	require.ErrorIs(t, repo.UpdateDocument(ctx, heldDoc), ErrForceRemovalTaskHeld)
	stored, err := repo.GetDocument(ctx, heldTaskID, heldDoc.Key)
	require.NoError(t, err)
	require.Equal(t, "before", stored.Content)

	foreignDoc.Content = "updated"
	require.NoError(t, repo.UpdateDocument(ctx, foreignDoc))
	stored, err = repo.GetDocument(ctx, foreignTaskID, foreignDoc.Key)
	require.NoError(t, err)
	require.Equal(t, "updated", stored.Content)
}
