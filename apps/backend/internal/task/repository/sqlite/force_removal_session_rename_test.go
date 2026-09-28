package sqlite

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

func TestClaimForceRemovalBlocksSessionRename(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-rename-ws", Name: "Force"}))
	for _, taskID := range []string{"force-rename-held", "force-rename-foreign"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: "force-rename-ws", Title: taskID}))
		require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{ID: taskID + "-session", TaskID: taskID, Name: taskID + "-before"}))
	}
	held, err := repo.GetTask(ctx, "force-rename-held")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: held.ID, WorkspaceID: held.WorkspaceID, TaskGeneration: held.UpdatedAt, AdmissionGeneration: "admission", OperationID: "rename", RequestDigest: "request", PreviewDigest: "preview"})
	require.NoError(t, err)

	err = repo.RenameTaskSession(ctx, "force-rename-held-session", "held-after")
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	heldSession, err := repo.GetTaskSession(ctx, "force-rename-held-session")
	require.NoError(t, err)
	require.Equal(t, "force-rename-held-before", heldSession.Name)

	require.NoError(t, repo.RenameTaskSession(ctx, "force-rename-foreign-session", "foreign-after"))
	foreignSession, err := repo.GetTaskSession(ctx, "force-rename-foreign-session")
	require.NoError(t, err)
	require.Equal(t, "foreign-after", foreignSession.Name)
	require.ErrorIs(t, repo.RenameTaskSession(ctx, "force-rename-missing-session", "missing"), models.ErrTaskSessionNotFound)
}
