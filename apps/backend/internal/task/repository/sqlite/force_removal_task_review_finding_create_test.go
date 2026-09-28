package sqlite

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

func TestClaimForceRemovalBlocksTaskReviewFindingBatchCreateWithoutPartialMutation(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForSessionTests(t)
	const heldTaskID = "force-review-finding-create-held"
	const foreignTaskID = "force-review-finding-create-foreign"
	seedReviewTask(t, ctx, repo, heldTaskID)
	seedReviewTask(t, ctx, repo, foreignTaskID)
	heldRun := newReviewRun(t, ctx, repo, heldTaskID)
	foreignRun := newReviewRun(t, ctx, repo, foreignTaskID)

	heldTask, err := repo.GetTask(ctx, heldTaskID)
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: heldTask.ID, WorkspaceID: heldTask.WorkspaceID, TaskGeneration: heldTask.UpdatedAt, AdmissionGeneration: "admission", OperationID: "review-finding-create", RequestDigest: "request", PreviewDigest: "preview"})
	require.NoError(t, err)

	heldFinding := finding(heldRun.ID, heldTaskID, "held.go", "held", 1)
	foreignFinding := finding(foreignRun.ID, foreignTaskID, "foreign.go", "foreign", 1)
	require.ErrorIs(t, repo.CreateTaskReviewFindings(ctx, []*models.TaskReviewFinding{foreignFinding, heldFinding}), ErrForceRemovalTaskHeld)
	for _, taskID := range []string{heldTaskID, foreignTaskID} {
		stored, listErr := repo.ListTaskReviewFindings(ctx, taskID)
		require.NoError(t, listErr)
		require.Empty(t, stored)
	}
}

func TestTaskReviewFindingBatchCreateAllowsForeignTask(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForSessionTests(t)
	const taskID = "force-review-finding-create-foreign"
	seedReviewTask(t, ctx, repo, taskID)
	run := newReviewRun(t, ctx, repo, taskID)
	f := finding(run.ID, taskID, "foreign.go", "foreign", 1)
	require.NoError(t, repo.CreateTaskReviewFindings(ctx, []*models.TaskReviewFinding{f}))
	stored, err := repo.ListTaskReviewFindings(ctx, taskID)
	require.NoError(t, err)
	require.Len(t, stored, 1)
}
