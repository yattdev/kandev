package sqlite

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

func TestTerminalProviderClaimFencesRotationAndMetadataReplacement(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForSessionTests(t)
	seedForMsgTest(t, repo, "task-terminal-claim", "session-terminal-claim", "turn-terminal-claim")
	require.NoError(t, repo.UpdateTaskSessionState(ctx, "session-terminal-claim", models.TaskSessionStateStarting, ""))
	require.NoError(t, repo.UpsertExecutorRunning(ctx, &models.ExecutorRunning{
		ID: "session-terminal-claim", SessionID: "session-terminal-claim", TaskID: "task-terminal-claim", AgentExecutionID: "exec-old",
	}))
	claim := models.TerminalProviderAccessClaim{
		TaskID: "task-terminal-claim", SessionID: "session-terminal-claim", AgentExecutionID: "exec-old",
		RequireExecution: true, ExpectedState: models.TaskSessionStateStarting, CheckErrorStamp: true,
	}
	stale := claim
	stale.AgentExecutionID = "exec-other"
	_, reserved, err := repo.ClaimProviderAccessTerminal(ctx, stale)
	require.NoError(t, err)
	require.False(t, reserved)

	claimID, reserved, err := repo.ClaimProviderAccessTerminal(ctx, claim)
	require.NoError(t, err)
	require.True(t, reserved)
	_, reserved, err = repo.ClaimProviderAccessTerminal(ctx, claim)
	require.NoError(t, err)
	require.False(t, reserved)
	session, err := repo.GetTaskSession(ctx, "session-terminal-claim")
	require.NoError(t, err)
	require.NotContains(t, session.Metadata, terminalProviderClaimKey)
	require.ErrorIs(t, repo.UpsertExecutorRunning(ctx, &models.ExecutorRunning{
		ID: "session-terminal-claim", SessionID: "session-terminal-claim", TaskID: "task-terminal-claim", AgentExecutionID: "exec-new",
	}), ErrTerminalProviderClaimPending)
	require.ErrorIs(t, repo.DeleteExecutorRunningBySessionID(ctx, "session-terminal-claim"), ErrTerminalProviderClaimPending)
	require.Error(t, repo.UpdateSessionMetadata(ctx, "session-terminal-claim", map[string]interface{}{"replacement": true}))
	changed, _, err := repo.UpdateTaskSessionStateIfCurrent(ctx, "session-terminal-claim",
		models.TaskSessionStateStarting, models.TaskSessionStateRunning, "")
	require.NoError(t, err)
	require.False(t, changed)
	changed, _, err = repo.UpdateTaskSessionStateIfCurrentClaim(ctx, "session-terminal-claim",
		models.TaskSessionStateStarting, models.TaskSessionStateFailed, "", "wrong-claim")
	require.NoError(t, err)
	require.False(t, changed)

	require.NoError(t, repo.ReleaseProviderAccessTerminal(ctx, "session-terminal-claim", claimID))
	require.NoError(t, repo.UpsertExecutorRunning(ctx, &models.ExecutorRunning{
		ID: "session-terminal-claim", SessionID: "session-terminal-claim", TaskID: "task-terminal-claim", AgentExecutionID: "exec-new",
	}))
	_, reserved, err = repo.ClaimProviderAccessTerminal(ctx, claim)
	require.NoError(t, err)
	require.False(t, reserved)
}

func TestTerminalProviderClaimOwnerCanCommitWhileOtherStateWritersCannot(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForSessionTests(t)
	seedForMsgTest(t, repo, "task-terminal-commit", "session-terminal-commit", "turn-terminal-commit")
	require.NoError(t, repo.UpdateTaskSessionState(ctx, "session-terminal-commit", models.TaskSessionStateRunning, ""))
	claimID, reserved, err := repo.ClaimProviderAccessTerminal(ctx, models.TerminalProviderAccessClaim{
		TaskID: "task-terminal-commit", SessionID: "session-terminal-commit",
		ExpectedState: models.TaskSessionStateRunning,
	})
	require.NoError(t, err)
	require.True(t, reserved)
	changed, _, err := repo.CancelActiveTaskSession(ctx, "session-terminal-commit", "other owner")
	require.NoError(t, err)
	require.False(t, changed)
	changed, _, err = repo.CancelActiveTaskSessionWithClaim(ctx, "session-terminal-commit", "terminal owner",
		models.TaskSessionStateRunning, claimID)
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, repo.ReleaseProviderAccessTerminal(ctx, "session-terminal-commit", claimID))
	session, err := repo.GetTaskSession(ctx, "session-terminal-commit")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateCancelled, session.State)
}
