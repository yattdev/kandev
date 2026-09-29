package sqlite

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

func TestClaimForceRemovalBlocksDocumentDelete(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	const workspaceID = "force-document-delete-workspace"
	const heldTaskID = "force-document-delete-held"
	const foreignTaskID = "force-document-delete-foreign"
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: workspaceID, Name: "Force"}))
	for _, taskID := range []string{heldTaskID, foreignTaskID} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: workspaceID, Title: taskID}))
	}
	for _, doc := range []*models.TaskDocument{{ID: "force-document-delete-held", TaskID: heldTaskID, Key: "notes", Type: "note"}, {ID: "force-document-delete-foreign", TaskID: foreignTaskID, Key: "notes", Type: "note"}} {
		require.NoError(t, repo.CreateDocument(ctx, doc))
	}
	heldTask, err := repo.GetTask(ctx, heldTaskID)
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: heldTask.ID, WorkspaceID: heldTask.WorkspaceID, TaskGeneration: heldTask.UpdatedAt, AdmissionGeneration: "admission", OperationID: "document-delete", RequestDigest: "request", PreviewDigest: "preview"})
	require.NoError(t, err)

	require.ErrorIs(t, repo.DeleteDocument(ctx, heldTaskID, "notes"), ErrForceRemovalTaskHeld)
	stored, err := repo.GetDocument(ctx, heldTaskID, "notes")
	require.NoError(t, err)
	require.NotNil(t, stored)
	require.NoError(t, repo.DeleteDocument(ctx, foreignTaskID, "notes"))
	stored, err = repo.GetDocument(ctx, foreignTaskID, "notes")
	require.NoError(t, err)
	require.Nil(t, stored)
}
