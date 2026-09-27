package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

func TestBulkTerminalWritersDeferClaimedSession(t *testing.T) {
	forms := map[string]func(context.Context, *Repository, models.ActiveSessionCancellationCandidate) (int, error){
		"task": func(ctx context.Context, repo *Repository, _ models.ActiveSessionCancellationCandidate) (int, error) {
			rows, err := repo.CancelActiveTaskSessionsByTaskID(ctx, "task-bulk-claim", "archive")
			return len(rows), err
		},
		"stale session": func(ctx context.Context, repo *Repository, _ models.ActiveSessionCancellationCandidate) (int, error) {
			row, err := repo.CancelRunningTaskSessionByID(ctx, "session-bulk-claim", "stale", time.Now().Add(time.Hour))
			if row != nil {
				return 1, err
			}
			return 0, err
		},
		"ids": func(ctx context.Context, repo *Repository, _ models.ActiveSessionCancellationCandidate) (int, error) {
			rows, err := repo.CancelActiveTaskSessionsByIDs(ctx, "task-bulk-claim", []string{"session-bulk-claim"}, "archive")
			return len(rows), err
		},
		"candidates": func(ctx context.Context, repo *Repository, candidate models.ActiveSessionCancellationCandidate) (int, error) {
			rows, err := repo.CancelActiveTaskSessionsByCandidates(ctx, "task-bulk-claim", []models.ActiveSessionCancellationCandidate{candidate}, "stale")
			return len(rows), err
		},
	}
	for name, cancel := range forms {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			repo := newRepoForSessionTests(t)
			seedForMsgTest(t, repo, "task-bulk-claim", "session-bulk-claim", "turn-bulk-claim")
			require.NoError(t, repo.UpdateTaskSessionState(ctx, "session-bulk-claim", models.TaskSessionStateRunning, ""))
			session, err := repo.GetTaskSession(ctx, "session-bulk-claim")
			require.NoError(t, err)
			candidate := models.ActiveSessionCancellationCandidate{
				SessionID: session.ID, ExpectedUpdatedAt: session.UpdatedAt,
				ExpectedTurnID: "turn-bulk-claim",
			}
			claimID, claimed, err := repo.ClaimProviderAccessTerminal(ctx, models.TerminalProviderAccessClaim{
				TaskID: "task-bulk-claim", SessionID: session.ID, ExpectedState: models.TaskSessionStateRunning,
			})
			require.NoError(t, err)
			require.True(t, claimed)
			count, err := cancel(ctx, repo, candidate)
			require.NoError(t, err)
			require.Zero(t, count)
			check, err := repo.GetTaskSession(ctx, session.ID)
			require.NoError(t, err)
			require.Equal(t, models.TaskSessionStateRunning, check.State)
			require.NoError(t, repo.ReleaseProviderAccessTerminal(ctx, session.ID, claimID))
			count, err = cancel(ctx, repo, candidate)
			require.NoError(t, err)
			require.Equal(t, 1, count)
		})
	}
}

func TestProviderTerminalClaimDefersPromptAndRecoveryWriters(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForSessionTests(t)
	seedForMsgTest(t, repo, "task-prompt-claim", "session-prompt-claim", "turn-prompt-claim")
	require.NoError(t, repo.UpdateTaskSessionState(ctx, "session-prompt-claim", models.TaskSessionStateWaitingForInput, ""))
	claimID, claimed, err := repo.ClaimProviderAccessTerminal(ctx, models.TerminalProviderAccessClaim{
		TaskID: "task-prompt-claim", SessionID: "session-prompt-claim",
		ExpectedState: models.TaskSessionStateWaitingForInput,
	})
	require.NoError(t, err)
	require.True(t, claimed)
	prompt, err := repo.ClaimPromptableTaskSessionIfActive(ctx, "session-prompt-claim")
	require.NoError(t, err)
	require.Equal(t, models.PromptableTaskSessionBusy, prompt.Status)
	require.NoError(t, repo.ReleaseProviderAccessTerminal(ctx, "session-prompt-claim", claimID))
	prompt, err = repo.ClaimPromptableTaskSessionIfActive(ctx, "session-prompt-claim")
	require.NoError(t, err)
	require.Equal(t, models.PromptableTaskSessionClaimed, prompt.Status)

	session, err := repo.GetTaskSession(ctx, "session-prompt-claim")
	require.NoError(t, err)
	claimID, claimed, err = repo.ClaimProviderAccessTerminal(ctx, models.TerminalProviderAccessClaim{
		TaskID: "task-prompt-claim", SessionID: session.ID, ExpectedState: models.TaskSessionStateRunning,
	})
	require.NoError(t, err)
	require.True(t, claimed)
	recovered, err := repo.RecoverTaskSessionByCandidate(ctx, models.ActiveSessionRecoveryCandidate{
		TaskID: "task-prompt-claim", SessionID: session.ID,
		ExpectedState: models.TaskSessionStateRunning, ExpectedUpdatedAt: session.UpdatedAt,
		ExpectedTurnID: "turn-prompt-claim",
	}, time.Time{})
	require.NoError(t, err)
	require.Nil(t, recovered)
	require.NoError(t, repo.ReleaseProviderAccessTerminal(ctx, session.ID, claimID))
}

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
