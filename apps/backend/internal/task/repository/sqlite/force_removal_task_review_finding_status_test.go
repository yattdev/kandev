package sqlite

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

func TestClaimForceRemovalBlocksTaskReviewFindingStatusTransition(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForSessionTests(t)
	const heldTaskID = "force-review-finding-status-held"
	const foreignTaskID = "force-review-finding-status-foreign"
	seedReviewTask(t, ctx, repo, heldTaskID)
	seedReviewTask(t, ctx, repo, foreignTaskID)
	heldRun := newReviewRun(t, ctx, repo, heldTaskID)
	foreignRun := newReviewRun(t, ctx, repo, foreignTaskID)
	heldFinding := finding(heldRun.ID, heldTaskID, "held.go", "held issue", 1)
	foreignFinding := finding(foreignRun.ID, foreignTaskID, "foreign.go", "foreign issue", 1)
	require.NoError(t, repo.CreateTaskReviewFindings(ctx, []*models.TaskReviewFinding{heldFinding, foreignFinding}))

	heldTask, err := repo.GetTask(ctx, heldTaskID)
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{
		TaskID: heldTask.ID, WorkspaceID: heldTask.WorkspaceID, TaskGeneration: heldTask.UpdatedAt,
		AdmissionGeneration: "admission", OperationID: "review-finding-status", RequestDigest: "request", PreviewDigest: "preview",
	})
	require.NoError(t, err)

	_, err = repo.TransitionTaskReviewFindingStatus(ctx, heldFinding.ID, models.ReviewFindingResolved)
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	stored, err := repo.GetTaskReviewFinding(ctx, heldFinding.ID)
	require.NoError(t, err)
	require.Equal(t, models.ReviewFindingOpen, stored.Status)
	require.Nil(t, stored.ResolvedAt)

	transitioned, err := repo.TransitionTaskReviewFindingStatus(ctx, foreignFinding.ID, models.ReviewFindingResolved)
	require.NoError(t, err)
	require.Equal(t, models.ReviewFindingResolved, transitioned.Status)
	require.NotNil(t, transitioned.ResolvedAt)
}
