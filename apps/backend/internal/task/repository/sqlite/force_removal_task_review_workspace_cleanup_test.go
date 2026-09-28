package sqlite

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

func TestClaimForceRemovalBlocksWorkspaceReviewCleanupWithoutPartialMutation(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForSessionTests(t)
	const heldTaskID = "force-review-workspace-held"
	const foreignTaskID = "force-review-workspace-foreign"
	seedReviewTask(t, ctx, repo, heldTaskID)
	seedReviewTask(t, ctx, repo, foreignTaskID)
	heldRun := newReviewRun(t, ctx, repo, heldTaskID)
	foreignRun := newReviewRun(t, ctx, repo, foreignTaskID)
	require.NoError(t, repo.CreateTaskReviewFindings(ctx, []*models.TaskReviewFinding{finding(heldRun.ID, heldTaskID, "held.go", "held", 1), finding(foreignRun.ID, foreignTaskID, "foreign.go", "foreign", 1)}))

	heldTask, err := repo.GetTask(ctx, heldTaskID)
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: heldTask.ID, WorkspaceID: heldTask.WorkspaceID, TaskGeneration: heldTask.UpdatedAt, AdmissionGeneration: "admission", OperationID: "review-workspace-cleanup", RequestDigest: "request", PreviewDigest: "preview"})
	require.NoError(t, err)

	require.ErrorIs(t, repo.DeleteTaskReviewByWorkspace(ctx, reviewTestWorkspaceID), ErrForceRemovalTaskHeld)
	for _, taskID := range []string{heldTaskID, foreignTaskID} {
		runs, listErr := repo.ListTaskReviewRuns(ctx, taskID, 10)
		require.NoError(t, listErr)
		require.Len(t, runs, 1)
	}
}
