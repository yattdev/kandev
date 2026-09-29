package sqlite

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

func TestClaimForceRemovalBlocksDocumentCreate(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	const workspaceID = "force-document-workspace"
	const heldTaskID = "force-document-held"
	const foreignTaskID = "force-document-foreign"
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: workspaceID, Name: "Force"}))
	for _, taskID := range []string{heldTaskID, foreignTaskID} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: workspaceID, Title: taskID}))
	}
	heldTask, err := repo.GetTask(ctx, heldTaskID)
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: heldTask.ID, WorkspaceID: heldTask.WorkspaceID, TaskGeneration: heldTask.UpdatedAt, AdmissionGeneration: "admission", OperationID: "document-create", RequestDigest: "request", PreviewDigest: "preview"})
	require.NoError(t, err)

	heldDoc := &models.TaskDocument{ID: "force-document-held", TaskID: heldTaskID, Key: "notes", Type: "note"}
	require.ErrorIs(t, repo.CreateDocument(ctx, heldDoc), ErrForceRemovalTaskHeld)
	stored, err := repo.GetDocument(ctx, heldTaskID, heldDoc.Key)
	require.NoError(t, err)
	require.Nil(t, stored)

	foreignDoc := &models.TaskDocument{ID: "force-document-foreign", TaskID: foreignTaskID, Key: "notes", Type: "note"}
	require.NoError(t, repo.CreateDocument(ctx, foreignDoc))
	stored, err = repo.GetDocument(ctx, foreignTaskID, foreignDoc.Key)
	require.NoError(t, err)
	require.NotNil(t, stored)
}
