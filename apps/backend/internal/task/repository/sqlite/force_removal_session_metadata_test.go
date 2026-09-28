package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kandev/kandev/internal/task/models"
)

func TestClaimForceRemovalBlocksDirectSessionMetadataKey(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{
		ID: "force-session-key-ws", Name: "Force",
	}))
	for _, taskID := range []string{"force-session-key-held", "force-session-key-foreign"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{
			ID: taskID, WorkspaceID: "force-session-key-ws", Title: taskID,
		}))
		require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{
			ID: taskID + "-session", TaskID: taskID,
		}))
	}

	held, err := repo.GetTask(ctx, "force-session-key-held")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{
		TaskID:              held.ID,
		WorkspaceID:         held.WorkspaceID,
		TaskGeneration:      held.UpdatedAt,
		AdmissionGeneration: "admission",
		OperationID:         "session-key-operation",
		RequestDigest:       "request",
		PreviewDigest:       "preview",
	})
	require.NoError(t, err)

	err = repo.SetSessionMetadataKey(ctx, "force-session-key-held-session", "marker", "held")
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	heldSession, err := repo.GetTaskSession(ctx, "force-session-key-held-session")
	require.NoError(t, err)
	require.NotContains(t, heldSession.Metadata, "marker")

	require.NoError(t, repo.SetSessionMetadataKey(
		ctx, "force-session-key-foreign-session", "marker", "foreign",
	))
	foreignSession, err := repo.GetTaskSession(ctx, "force-session-key-foreign-session")
	require.NoError(t, err)
	require.Equal(t, "foreign", foreignSession.Metadata["marker"])
}
