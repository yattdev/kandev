package sqlite

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

func TestClaimForceRemovalBlocksDocumentRevisionInsert(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	const workspaceID = "force-document-revision-workspace"
	const heldTaskID = "force-document-revision-held"
	const foreignTaskID = "force-document-revision-foreign"
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: workspaceID, Name: "Force"}))
	for _, taskID := range []string{heldTaskID, foreignTaskID} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: workspaceID, Title: taskID}))
	}
	heldTask, err := repo.GetTask(ctx, heldTaskID)
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: heldTask.ID, WorkspaceID: heldTask.WorkspaceID, TaskGeneration: heldTask.UpdatedAt, AdmissionGeneration: "admission", OperationID: "document-revision-insert", RequestDigest: "request", PreviewDigest: "preview"})
	require.NoError(t, err)

	held := &models.TaskDocumentRevision{ID: "force-document-revision-held", TaskID: heldTaskID, DocumentKey: "notes", RevisionNumber: 1}
	require.ErrorIs(t, repo.InsertDocumentRevision(ctx, held), ErrForceRemovalTaskHeld)
	stored, err := repo.GetDocumentRevision(ctx, held.ID)
	require.NoError(t, err)
	require.Nil(t, stored)

	foreign := &models.TaskDocumentRevision{ID: "force-document-revision-foreign", TaskID: foreignTaskID, DocumentKey: "notes", RevisionNumber: 1}
	require.NoError(t, repo.InsertDocumentRevision(ctx, foreign))
	stored, err = repo.GetDocumentRevision(ctx, foreign.ID)
	require.NoError(t, err)
	require.NotNil(t, stored)
}
