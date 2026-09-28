package sqlite

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

func TestClaimForceRemovalBlocksTaskReviewFindingSupersession(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForSessionTests(t)
	const heldTaskID = "force-review-finding-supersede-held"
	const foreignTaskID = "force-review-finding-supersede-foreign"
	seedReviewTask(t, ctx, repo, heldTaskID)
	seedReviewTask(t, ctx, repo, foreignTaskID)

	heldOldRun := newReviewRun(t, ctx, repo, heldTaskID)
	heldNewRun := newReviewRun(t, ctx, repo, heldTaskID)
	heldOld := finding(heldOldRun.ID, heldTaskID, "held.go", "duplicate", 1)
	heldFresh := finding(heldNewRun.ID, heldTaskID, "held.go", "duplicate", 1)
	foreignOldRun := newReviewRun(t, ctx, repo, foreignTaskID)
	foreignNewRun := newReviewRun(t, ctx, repo, foreignTaskID)
	foreignOld := finding(foreignOldRun.ID, foreignTaskID, "foreign.go", "duplicate", 1)
	foreignFresh := finding(foreignNewRun.ID, foreignTaskID, "foreign.go", "duplicate", 1)
	require.NoError(t, repo.CreateTaskReviewFindings(ctx, []*models.TaskReviewFinding{heldOld, heldFresh, foreignOld, foreignFresh}))

	heldTask, err := repo.GetTask(ctx, heldTaskID)
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{
		TaskID: heldTask.ID, WorkspaceID: heldTask.WorkspaceID, TaskGeneration: heldTask.UpdatedAt,
		AdmissionGeneration: "admission", OperationID: "review-finding-supersede", RequestDigest: "request", PreviewDigest: "preview",
	})
	require.NoError(t, err)

	_, err = repo.DeleteSupersededTaskReviewFindings(ctx, heldTaskID, heldNewRun.ID, SupersedeKeysFor([]*models.TaskReviewFinding{heldFresh}))
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	_, err = repo.GetTaskReviewFinding(ctx, heldOld.ID)
	require.NoError(t, err)

	deleted, err := repo.DeleteSupersededTaskReviewFindings(ctx, foreignTaskID, foreignNewRun.ID, SupersedeKeysFor([]*models.TaskReviewFinding{foreignFresh}))
	require.NoError(t, err)
	require.Equal(t, []string{foreignOld.ID}, deleted)
	_, err = repo.GetTaskReviewFinding(ctx, foreignOld.ID)
	require.ErrorIs(t, err, models.ErrTaskReviewFindingNotFound)

	deleted, err = repo.DeleteSupersededTaskReviewFindings(ctx, foreignTaskID, foreignNewRun.ID, SupersedeKeysFor([]*models.TaskReviewFinding{foreignFresh}))
	require.NoError(t, err)
	require.Empty(t, deleted)
}
