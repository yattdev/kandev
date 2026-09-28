package sqlite

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

func TestClaimForceRemovalBlocksTaskReviewRunUpdate(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForSessionTests(t)
	const heldTaskID = "force-review-run-update-held"
	const foreignTaskID = "force-review-run-update-foreign"
	seedReviewTask(t, ctx, repo, heldTaskID)
	seedReviewTask(t, ctx, repo, foreignTaskID)
	heldRun := newReviewRun(t, ctx, repo, heldTaskID)
	foreignRun := newReviewRun(t, ctx, repo, foreignTaskID)

	heldTask, err := repo.GetTask(ctx, heldTaskID)
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{
		TaskID: heldTask.ID, WorkspaceID: heldTask.WorkspaceID, TaskGeneration: heldTask.UpdatedAt,
		AdmissionGeneration: "admission", OperationID: "review-run-update", RequestDigest: "request", PreviewDigest: "preview",
	})
	require.NoError(t, err)

	heldRun.Status = models.ReviewRunCompleted
	heldRun.Summary = "must not persist"
	require.ErrorIs(t, repo.UpdateTaskReviewRun(ctx, heldRun), ErrForceRemovalTaskHeld)
	stored, err := repo.GetTaskReviewRun(ctx, heldRun.ID)
	require.NoError(t, err)
	require.Equal(t, models.ReviewRunPending, stored.Status)
	require.Empty(t, stored.Summary)

	foreignRun.Status = models.ReviewRunCompleted
	foreignRun.Summary = "completed"
	require.NoError(t, repo.UpdateTaskReviewRun(ctx, foreignRun))
	stored, err = repo.GetTaskReviewRun(ctx, foreignRun.ID)
	require.NoError(t, err)
	require.Equal(t, models.ReviewRunCompleted, stored.Status)
	require.Equal(t, "completed", stored.Summary)
}
