package sqlite

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

func TestClaimForceRemovalBlocksDocumentRevisionWrite(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	const workspaceID = "force-document-revision-write-workspace"
	const heldTaskID = "force-document-revision-write-held"
	const foreignTaskID = "force-document-revision-write-foreign"
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: workspaceID, Name: "Force"}))
	for _, taskID := range []string{heldTaskID, foreignTaskID} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: workspaceID, Title: taskID}))
	}
	heldHead := &models.TaskDocument{ID: "force-document-revision-write-held", TaskID: heldTaskID, Key: "notes", Type: "note", Content: "before"}
	heldRevision := &models.TaskDocumentRevision{TaskID: heldTaskID, DocumentKey: "notes", Content: "before"}
	require.NoError(t, repo.WriteDocumentRevision(ctx, heldHead, heldRevision, nil))

	heldTask, err := repo.GetTask(ctx, heldTaskID)
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: heldTask.ID, WorkspaceID: heldTask.WorkspaceID, TaskGeneration: heldTask.UpdatedAt, AdmissionGeneration: "admission", OperationID: "document-revision-write", RequestDigest: "request", PreviewDigest: "preview"})
	require.NoError(t, err)
	heldHead.Content = "must not persist"
	blocked := &models.TaskDocumentRevision{TaskID: heldTaskID, DocumentKey: "notes", Content: "must not persist"}
	require.ErrorIs(t, repo.WriteDocumentRevision(ctx, heldHead, blocked, nil), ErrForceRemovalTaskHeld)
	stored, err := repo.GetDocument(ctx, heldTaskID, "notes")
	require.NoError(t, err)
	require.Equal(t, "before", stored.Content)
	history, err := repo.ListDocumentRevisions(ctx, heldTaskID, "notes", 0)
	require.NoError(t, err)
	require.Len(t, history, 1)

	foreignHead := &models.TaskDocument{ID: "force-document-revision-write-foreign", TaskID: foreignTaskID, Key: "notes", Type: "note", Content: "foreign"}
	foreignRevision := &models.TaskDocumentRevision{TaskID: foreignTaskID, DocumentKey: "notes", Content: "foreign"}
	require.NoError(t, repo.WriteDocumentRevision(ctx, foreignHead, foreignRevision, nil))
	history, err = repo.ListDocumentRevisions(ctx, foreignTaskID, "notes", 0)
	require.NoError(t, err)
	require.Len(t, history, 1)
}
