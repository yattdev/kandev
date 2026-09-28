package sqlite

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

func TestClaimForceRemovalBlocksTaskReviewRunCreate(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForSessionTests(t)
	const heldTaskID = "force-review-run-held"
	const foreignTaskID = "force-review-run-foreign"
	seedReviewTask(t, ctx, repo, heldTaskID)
	seedReviewTask(t, ctx, repo, foreignTaskID)

	heldTask, err := repo.GetTask(ctx, heldTaskID)
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{
		TaskID: heldTask.ID, WorkspaceID: heldTask.WorkspaceID, TaskGeneration: heldTask.UpdatedAt,
		AdmissionGeneration: "admission", OperationID: "review-run-create", RequestDigest: "request", PreviewDigest: "preview",
	})
	require.NoError(t, err)

	heldRun := &models.TaskReviewRun{TaskID: heldTaskID, SessionID: "held-session"}
	require.ErrorIs(t, repo.CreateTaskReviewRun(ctx, heldRun), ErrForceRemovalTaskHeld)
	runs, err := repo.ListTaskReviewRuns(ctx, heldTaskID, 10)
	require.NoError(t, err)
	require.Empty(t, runs)

	foreignRun := &models.TaskReviewRun{TaskID: foreignTaskID, SessionID: "foreign-session"}
	require.NoError(t, repo.CreateTaskReviewRun(ctx, foreignRun))
	runs, err = repo.ListTaskReviewRuns(ctx, foreignTaskID, 10)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	require.Equal(t, foreignRun.ID, runs[0].ID)
}
