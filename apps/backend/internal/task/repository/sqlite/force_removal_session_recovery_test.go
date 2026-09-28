package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/kandev/kandev/internal/task/models"
)

// @covers AC-TASKS-SAFE-FORCE-REMOVAL-004
func TestClaimForceRemovalBlocksCandidateSessionRecovery(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-recovery-ws", Name: "Force"}))
	for _, taskID := range []string{"force-recovery-held", "force-recovery-foreign", "force-recovery-terminal"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: "force-recovery-ws", Title: taskID}))
		state := models.TaskSessionStateRunning
		if taskID == "force-recovery-terminal" {
			state = models.TaskSessionStateCompleted
		}
		require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{ID: taskID + "-session", TaskID: taskID, State: state}))
	}

	held, err := repo.GetTask(ctx, "force-recovery-held")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: held.ID, WorkspaceID: held.WorkspaceID, TaskGeneration: held.UpdatedAt, AdmissionGeneration: "admission", OperationID: "candidate-recovery", RequestDigest: "request", PreviewDigest: "preview"})
	require.NoError(t, err)

	heldSession, err := repo.GetTaskSession(ctx, "force-recovery-held-session")
	require.NoError(t, err)
	recovered, err := repo.RecoverTaskSessionByCandidate(ctx, models.ActiveSessionRecoveryCandidate{TaskID: held.ID, SessionID: heldSession.ID, ExpectedState: heldSession.State, ExpectedUpdatedAt: heldSession.UpdatedAt}, time.Now().UTC().Add(time.Minute))
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	require.Nil(t, recovered)
	heldSession, err = repo.GetTaskSession(ctx, heldSession.ID)
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateRunning, heldSession.State)
	require.False(t, models.HasInterruptedRecoveryPending(heldSession.Metadata))

	foreignSession, err := repo.GetTaskSession(ctx, "force-recovery-foreign-session")
	require.NoError(t, err)
	recovered, err = repo.RecoverTaskSessionByCandidate(ctx, models.ActiveSessionRecoveryCandidate{TaskID: foreignSession.TaskID, SessionID: foreignSession.ID, ExpectedState: foreignSession.State, ExpectedUpdatedAt: foreignSession.UpdatedAt}, time.Now().UTC().Add(time.Minute))
	require.NoError(t, err)
	require.NotNil(t, recovered)
	require.Equal(t, models.TaskSessionStateWaitingForInput, recovered.State)
	require.True(t, models.HasInterruptedRecoveryPending(recovered.Metadata))

	terminalSession, err := repo.GetTaskSession(ctx, "force-recovery-terminal-session")
	require.NoError(t, err)
	recovered, err = repo.RecoverTaskSessionByCandidate(ctx, models.ActiveSessionRecoveryCandidate{TaskID: terminalSession.TaskID, SessionID: terminalSession.ID, ExpectedState: models.TaskSessionStateRunning, ExpectedUpdatedAt: terminalSession.UpdatedAt}, time.Now().UTC().Add(time.Minute))
	require.NoError(t, err)
	require.Nil(t, recovered)
}
