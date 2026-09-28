package sqlite

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

func TestClaimForceRemovalBlocksInFlightReviewRunCancellationWithoutPartialMutation(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForSessionTests(t)
	const heldTaskID = "force-review-run-cancel-held"
	const foreignTaskID = "force-review-run-cancel-foreign"
	seedReviewTask(t, ctx, repo, heldTaskID)
	seedReviewTask(t, ctx, repo, foreignTaskID)
	heldRun := newReviewRun(t, ctx, repo, heldTaskID)
	foreignRun := newReviewRun(t, ctx, repo, foreignTaskID)

	heldTask, err := repo.GetTask(ctx, heldTaskID)
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{
		TaskID: heldTask.ID, WorkspaceID: heldTask.WorkspaceID, TaskGeneration: heldTask.UpdatedAt,
		AdmissionGeneration: "admission", OperationID: "review-run-cancel", RequestDigest: "request", PreviewDigest: "preview",
	})
	require.NoError(t, err)

	_, err = repo.CancelInFlightTaskReviewRuns(ctx)
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	for _, runID := range []string{heldRun.ID, foreignRun.ID} {
		stored, getErr := repo.GetTaskReviewRun(ctx, runID)
		require.NoError(t, getErr)
		require.Equal(t, models.ReviewRunPending, stored.Status)
		require.Nil(t, stored.CompletedAt)
	}
}

func TestInFlightReviewRunCancellationAllowsForeignTask(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForSessionTests(t)
	const taskID = "force-review-run-cancel-progress"
	seedReviewTask(t, ctx, repo, taskID)
	run := newReviewRun(t, ctx, repo, taskID)

	cancelled, err := repo.CancelInFlightTaskReviewRuns(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, cancelled)
	stored, err := repo.GetTaskReviewRun(ctx, run.ID)
	require.NoError(t, err)
	require.Equal(t, models.ReviewRunCancelled, stored.Status)
	require.NotNil(t, stored.CompletedAt)
}

func TestInFlightReviewRunOwnerOrderIsStable(t *testing.T) {
	forward := []string{"task-z", "task-a", "task-m", "task-a"}
	reverse := []string{"task-a", "task-m", "task-a", "task-z"}

	require.Equal(t, []string{"task-a", "task-m", "task-z"}, orderedReviewRunTaskIDs(forward))
	require.Equal(t, orderedReviewRunTaskIDs(forward), orderedReviewRunTaskIDs(reverse))
}
