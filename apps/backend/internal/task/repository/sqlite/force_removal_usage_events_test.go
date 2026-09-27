package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kandev/kandev/internal/task/models"
)

func TestClaimForceRemovalBlocksUsageEventLedgerAndRollup(t *testing.T) {
	repo := newUsageEventsTestRepo(t)
	ctx := context.Background()
	createUsageEventsTestTask(t, repo, "held-usage-task")
	createUsageEventsTestSession(t, repo, "held-usage-session", "held-usage-task")
	createUsageEventsTestTask(t, repo, "foreign-usage-task")
	createUsageEventsTestSession(t, repo, "foreign-usage-session", "foreign-usage-task")

	heldTask, err := repo.GetTask(ctx, "held-usage-task")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{
		TaskID:              heldTask.ID,
		WorkspaceID:         heldTask.WorkspaceID,
		TaskGeneration:      heldTask.UpdatedAt,
		AdmissionGeneration: "admission",
		OperationID:         "usage-event-operation",
		RequestDigest:       "request",
		PreviewDigest:       "preview",
	})
	require.NoError(t, err)

	err = repo.CreateTaskUsageEvent(ctx, newTestUsageEvent("held-usage-event", heldTask.ID, "held-usage-session"))
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	require.Zero(t, countTaskUsageEventRows(t, repo))
	tokensIn, tokensCachedIn, tokensOut, costSubcents := readTaskSessionRollup(t, repo, "held-usage-session")
	require.Equal(t, int64(0), tokensIn)
	require.Equal(t, int64(0), tokensCachedIn)
	require.Equal(t, int64(0), tokensOut)
	require.Equal(t, int64(0), costSubcents)

	require.NoError(t, repo.CreateTaskUsageEvent(ctx, newTestUsageEvent("foreign-usage-event", "foreign-usage-task", "foreign-usage-session")))
	require.Equal(t, 1, countTaskUsageEventRows(t, repo))
	tokensIn, tokensCachedIn, tokensOut, costSubcents = readTaskSessionRollup(t, repo, "foreign-usage-session")
	require.Equal(t, int64(100), tokensIn)
	require.Equal(t, int64(25), tokensCachedIn)
	require.Equal(t, int64(30), tokensOut)
	require.Equal(t, int64(42), costSubcents)
}
