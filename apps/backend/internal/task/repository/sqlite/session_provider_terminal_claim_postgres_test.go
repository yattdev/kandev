package sqlite

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestPostgresTerminalProviderClaimFencesExecutionRotation(t *testing.T) {
	db := testutil.OpenIsolatedPostgres(t, testutil.PostgresDSNFromEnv(t))
	repo, err := NewWithDB(db, db, nil)
	require.NoError(t, err)
	ctx := context.Background()
	const taskID, sessionID = "task-terminal-claim-pg", "session-terminal-claim-pg"
	seedPostgresTask(t, repo, taskID)
	require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{
		ID: sessionID, TaskID: taskID, State: models.TaskSessionStateStarting,
	}))
	require.NoError(t, repo.UpsertExecutorRunning(ctx, &models.ExecutorRunning{
		ID: sessionID, SessionID: sessionID, TaskID: taskID, AgentExecutionID: "exec-old",
	}))
	claim := models.TerminalProviderAccessClaim{
		TaskID: taskID, SessionID: sessionID, AgentExecutionID: "exec-old",
		RequireExecution: true, ExpectedState: models.TaskSessionStateStarting, CheckErrorStamp: true,
		TargetState: models.TaskSessionStateFailed, ErrorMessage: "provider stopped",
	}
	claimID, reserved, err := repo.ClaimProviderAccessTerminal(ctx, claim)
	require.NoError(t, err)
	require.True(t, reserved)
	pending, err := repo.ListPendingProviderAccessTerminalClaims(ctx)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.Equal(t, claimID, pending[0].ClaimID)
	require.Equal(t, claim.TargetState, pending[0].TargetState)
	cancelled, err := repo.CancelActiveTaskSessionsByTaskID(ctx, taskID, "archive")
	require.NoError(t, err)
	require.Empty(t, cancelled)
	require.ErrorIs(t, repo.UpsertExecutorRunning(ctx, &models.ExecutorRunning{
		ID: sessionID, SessionID: sessionID, TaskID: taskID, AgentExecutionID: "exec-new",
	}), ErrTerminalProviderClaimPending)
	require.NoError(t, repo.ReleaseProviderAccessTerminal(ctx, sessionID, claimID))
	pending, err = repo.ListPendingProviderAccessTerminalClaims(ctx)
	require.NoError(t, err)
	require.Empty(t, pending)
	require.NoError(t, repo.UpsertExecutorRunning(ctx, &models.ExecutorRunning{
		ID: sessionID, SessionID: sessionID, TaskID: taskID, AgentExecutionID: "exec-new",
	}))
	_, reserved, err = repo.ClaimProviderAccessTerminal(ctx, claim)
	require.NoError(t, err)
	require.False(t, reserved)
	claim.AgentExecutionID = "exec-new"
	claimID, reserved, err = repo.ClaimProviderAccessTerminal(ctx, claim)
	require.NoError(t, err)
	require.True(t, reserved)
	changed, _, err := repo.CommitBootstrapFailureIfCurrentExecutionClaim(ctx,
		taskID, sessionID, "exec-new", models.TaskSessionStateStarting, "",
		models.LastAgentError{Message: "terminal owner", StampValue: "accepted-pg"}, claimID)
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, repo.ReleaseProviderAccessTerminal(ctx, sessionID, claimID))
}
