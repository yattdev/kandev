package sqlite

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

func TestClaimForceRemovalBlocksSessionBaseReset(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-base-reset-ws", Name: "Force"}))
	for _, taskID := range []string{"force-base-reset-held", "force-base-reset-foreign"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: "force-base-reset-ws", Title: taskID}))
		require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{
			ID: taskID + "-session", TaskID: taskID, RepositoryID: "repo", BaseBranch: "main", BaseCommitSHA: taskID + "-sha",
		}))
	}

	heldTask, err := repo.GetTask(ctx, "force-base-reset-held")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{
		TaskID: heldTask.ID, WorkspaceID: heldTask.WorkspaceID, TaskGeneration: heldTask.UpdatedAt,
		AdmissionGeneration: "admission", OperationID: "base-reset", RequestDigest: "request", PreviewDigest: "preview",
	})
	require.NoError(t, err)

	_, err = repo.ResetTaskSessionBasesForRepository(ctx, heldTask.ID, "repo", "held-after")
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	heldSession, err := repo.GetTaskSession(ctx, "force-base-reset-held-session")
	require.NoError(t, err)
	require.Equal(t, "main", heldSession.BaseBranch)
	require.Equal(t, "force-base-reset-held-sha", heldSession.BaseCommitSHA)

	rows, err := repo.ResetTaskSessionBasesForRepository(ctx, "force-base-reset-foreign", "repo", "foreign-after")
	require.NoError(t, err)
	require.EqualValues(t, 1, rows)
	foreignSession, err := repo.GetTaskSession(ctx, "force-base-reset-foreign-session")
	require.NoError(t, err)
	require.Equal(t, "foreign-after", foreignSession.BaseBranch)
	require.Empty(t, foreignSession.BaseCommitSHA)
}
