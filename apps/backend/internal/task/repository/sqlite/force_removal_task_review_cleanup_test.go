package sqlite

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

func TestClaimForceRemovalBlocksTaskReviewCleanup(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForSessionTests(t)
	const heldTaskID = "force-review-cleanup-held"
	const foreignTaskID = "force-review-cleanup-foreign"
	seedReviewTask(t, ctx, repo, heldTaskID)
	seedReviewTask(t, ctx, repo, foreignTaskID)
	heldRun := newReviewRun(t, ctx, repo, heldTaskID)
	foreignRun := newReviewRun(t, ctx, repo, foreignTaskID)
	heldFinding := finding(heldRun.ID, heldTaskID, "held.go", "held", 1)
	foreignFinding := finding(foreignRun.ID, foreignTaskID, "foreign.go", "foreign", 1)
	require.NoError(t, repo.CreateTaskReviewFindings(ctx, []*models.TaskReviewFinding{heldFinding, foreignFinding}))

	heldTask, err := repo.GetTask(ctx, heldTaskID)
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: heldTask.ID, WorkspaceID: heldTask.WorkspaceID, TaskGeneration: heldTask.UpdatedAt, AdmissionGeneration: "admission", OperationID: "review-cleanup", RequestDigest: "request", PreviewDigest: "preview"})
	require.NoError(t, err)

	require.ErrorIs(t, repo.DeleteTaskReviewByTask(ctx, heldTaskID), ErrForceRemovalTaskHeld)
	_, err = repo.GetTaskReviewRun(ctx, heldRun.ID)
	require.NoError(t, err)
	_, err = repo.GetTaskReviewFinding(ctx, heldFinding.ID)
	require.NoError(t, err)

	require.NoError(t, repo.DeleteTaskReviewByTask(ctx, foreignTaskID))
	_, err = repo.GetTaskReviewRun(ctx, foreignRun.ID)
	require.ErrorIs(t, err, models.ErrTaskReviewRunNotFound)
	_, err = repo.GetTaskReviewFinding(ctx, foreignFinding.ID)
	require.ErrorIs(t, err, models.ErrTaskReviewFindingNotFound)
	require.NoError(t, repo.DeleteTaskReviewByTask(ctx, "missing"))
}
