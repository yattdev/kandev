package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kandev/kandev/internal/task/models"
)

// @covers AC-TASKS-SAFE-FORCE-REMOVAL-004
func TestClaimForceRemovalBlocksCandidateSessionCancellation(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-candidate-ws", Name: "Force"}))
	for _, taskID := range []string{"force-candidate-held", "force-candidate-foreign"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: "force-candidate-ws", Title: taskID}))
		require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{ID: taskID + "-running", TaskID: taskID, State: models.TaskSessionStateRunning}))
	}
	require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{ID: "force-candidate-foreign-completed", TaskID: "force-candidate-foreign", State: models.TaskSessionStateCompleted}))
	held, err := repo.GetTask(ctx, "force-candidate-held")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{
		TaskID: held.ID, WorkspaceID: held.WorkspaceID, TaskGeneration: held.UpdatedAt,
		AdmissionGeneration: "admission", OperationID: "candidate-cancel",
		RequestDigest: "request", PreviewDigest: "preview",
	})
	require.NoError(t, err)

	heldSession, err := repo.GetTaskSession(ctx, "force-candidate-held-running")
	require.NoError(t, err)
	heldCandidate := models.ActiveSessionCancellationCandidate{SessionID: heldSession.ID, ExpectedUpdatedAt: heldSession.UpdatedAt}
	cancelled, err := repo.CancelActiveTaskSessionsByCandidates(ctx, held.ID, []models.ActiveSessionCancellationCandidate{heldCandidate}, "held")
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	require.Empty(t, cancelled)
	heldSession, err = repo.GetTaskSession(ctx, heldSession.ID)
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateRunning, heldSession.State)

	foreignSession, err := repo.GetTaskSession(ctx, "force-candidate-foreign-running")
	require.NoError(t, err)
	completedSession, err := repo.GetTaskSession(ctx, "force-candidate-foreign-completed")
	require.NoError(t, err)
	foreignCandidates := []models.ActiveSessionCancellationCandidate{
		{SessionID: foreignSession.ID, ExpectedUpdatedAt: foreignSession.UpdatedAt},
		{SessionID: completedSession.ID, ExpectedUpdatedAt: completedSession.UpdatedAt},
	}
	cancelled, err = repo.CancelActiveTaskSessionsByCandidates(ctx, "force-candidate-foreign", foreignCandidates, "foreign")
	require.NoError(t, err)
	require.Len(t, cancelled, 1)
	require.Equal(t, foreignSession.ID, cancelled[0].ID)
	completedSession, err = repo.GetTaskSession(ctx, completedSession.ID)
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateCompleted, completedSession.State)

	cancelled, err = repo.CancelActiveTaskSessionsByCandidates(ctx, "force-candidate-foreign", foreignCandidates, "stale")
	require.NoError(t, err)
	require.Empty(t, cancelled)
}
