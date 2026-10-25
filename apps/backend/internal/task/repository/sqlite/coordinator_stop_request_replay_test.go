package sqlite

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/testutil"
	"github.com/stretchr/testify/require"
)

// @covers AC-STOP-FENCE-005
func TestListPendingCoordinatorStopRequestsIncludesIncompleteResults(t *testing.T) {
	assertCoordinatorStopRequestReplaySelection(t, newRepoForSessionTests(t))
}

// @covers AC-STOP-FENCE-005, AC-STOP-FENCE-008
func TestPostgresListPendingCoordinatorStopRequestsIncludesIncompleteResults(t *testing.T) {
	db := testutil.OpenIsolatedPostgres(t, testutil.PostgresDSNFromEnv(t))
	repo, err := NewWithDB(db, db, nil)
	require.NoError(t, err)
	assertCoordinatorStopRequestReplaySelection(t, repo)
}

func assertCoordinatorStopRequestReplaySelection(t *testing.T, repo *Repository) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: "replay-task", Title: "replay"}))
	require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: "replay-parent", Title: "parent"}))
	for _, operationID := range []string{"pending", "incomplete", "finished"} {
		_, _, err := repo.CaptureCoordinatorStopRequest(ctx, models.CoordinatorStopRequest{TaskID: "replay-task", ParentTaskID: "replay-parent", OperationID: operationID})
		require.NoError(t, err)
	}
	require.NoError(t, repo.CompleteCoordinatorStopRequest(ctx, "replay-task", "incomplete", "replay-parent", "incomplete"))
	require.NoError(t, repo.CompleteCoordinatorStopRequest(ctx, "replay-task", "finished", "replay-parent", "not_running"))
	requests, err := repo.ListPendingCoordinatorStopRequests(ctx)
	require.NoError(t, err)
	require.Len(t, requests, 2)
	ids := make([]string, 0, len(requests))
	for _, request := range requests {
		ids = append(ids, request.OperationID)
	}
	require.ElementsMatch(t, []string{"pending", "incomplete"}, ids)
}
