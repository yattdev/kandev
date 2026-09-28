package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kandev/kandev/internal/task/models"
)

// @covers AC-TASKS-SAFE-FORCE-REMOVAL-004
func TestForceRemovalHoldsDynamicRouteProjection(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "route-projection-ws", Name: "Route"}))
	for _, id := range []string{"route-held", "route-foreign", "route-stale"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: id, WorkspaceID: "route-projection-ws", Title: id}))
		require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{
			ID: id + "-session", TaskID: id, State: models.TaskSessionStateRunning,
			RouteGeneration: 7, RouteState: "starting", RouteReason: "original",
		}))
	}
	held, err := repo.GetTask(ctx, "route-held")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{
		TaskID: held.ID, WorkspaceID: held.WorkspaceID, TaskGeneration: held.UpdatedAt,
		AdmissionGeneration: "admission", OperationID: "route-projection",
		RequestDigest: "request", PreviewDigest: "preview",
	})
	require.NoError(t, err)

	changed, _, err := repo.UpdateTaskSessionDynamicRouteIfCurrent(ctx, "route-held-session", 7, "starting", "action_required", "blocked")
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	require.False(t, changed)
	heldSession, err := repo.GetTaskSession(ctx, "route-held-session")
	require.NoError(t, err)
	require.Equal(t, "starting", heldSession.RouteState)
	require.Equal(t, "original", heldSession.RouteReason)

	changed, _, err = repo.UpdateTaskSessionDynamicRouteIfCurrent(ctx, "route-foreign-session", 7, "starting", "active", "ready")
	require.NoError(t, err)
	require.True(t, changed)
	foreignSession, err := repo.GetTaskSession(ctx, "route-foreign-session")
	require.NoError(t, err)
	require.Equal(t, "active", foreignSession.RouteState)
	require.Equal(t, "ready", foreignSession.RouteReason)

	changed, _, err = repo.UpdateTaskSessionDynamicRouteIfCurrent(ctx, "route-stale-session", 6, "starting", "active", "stale")
	require.NoError(t, err)
	require.False(t, changed)
	staleSession, err := repo.GetTaskSession(ctx, "route-stale-session")
	require.NoError(t, err)
	require.Equal(t, "starting", staleSession.RouteState)
	require.Equal(t, "original", staleSession.RouteReason)
}
