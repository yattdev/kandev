package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kandev/kandev/internal/task/models"
)

func TestClaimForceRemovalBlocksSessionBaseCommitUpdate(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-base-commit-ws", Name: "Force"}))
	for _, taskID := range []string{"force-base-commit-held", "force-base-commit-foreign"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: "force-base-commit-ws", Title: taskID}))
		require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{
			ID: taskID + "-session", TaskID: taskID, BaseCommitSHA: taskID + "-before",
		}))
	}

	held, err := repo.GetTask(ctx, "force-base-commit-held")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{
		TaskID: held.ID, WorkspaceID: held.WorkspaceID, TaskGeneration: held.UpdatedAt,
		AdmissionGeneration: "admission", OperationID: "base-commit", RequestDigest: "request", PreviewDigest: "preview",
	})
	require.NoError(t, err)

	err = repo.UpdateTaskSessionBaseCommit(ctx, "force-base-commit-held-session", "held-after")
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	heldSession, err := repo.GetTaskSession(ctx, "force-base-commit-held-session")
	require.NoError(t, err)
	require.Equal(t, "force-base-commit-held-before", heldSession.BaseCommitSHA)

	require.NoError(t, repo.UpdateTaskSessionBaseCommit(ctx, "force-base-commit-foreign-session", "foreign-after"))
	foreignSession, err := repo.GetTaskSession(ctx, "force-base-commit-foreign-session")
	require.NoError(t, err)
	require.Equal(t, "foreign-after", foreignSession.BaseCommitSHA)

	err = repo.UpdateTaskSessionBaseCommit(ctx, "force-base-commit-missing-session", "missing-after")
	require.ErrorIs(t, err, models.ErrTaskSessionNotFound)
}
