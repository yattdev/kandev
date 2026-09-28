package sqlite

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

func TestClaimForceRemovalBlocksSessionPrimaryAndPreservesPredicates(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-primary-ws", Name: "Force"}))

	for _, taskID := range []string{"force-primary-held", "force-primary-foreign", "force-primary-stale-route", "force-primary-terminal"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: "force-primary-ws", Title: taskID}))
	}
	for _, session := range []*models.TaskSession{
		{ID: "force-primary-held-current", TaskID: "force-primary-held", IsPrimary: true, State: models.TaskSessionStateRunning},
		{ID: "force-primary-held-target", TaskID: "force-primary-held", State: models.TaskSessionStateCreated},
		{ID: "force-primary-foreign-current", TaskID: "force-primary-foreign", IsPrimary: true, State: models.TaskSessionStateRunning},
		{ID: "force-primary-foreign-target", TaskID: "force-primary-foreign", State: models.TaskSessionStateCreated},
		{ID: "force-primary-stale-current", TaskID: "force-primary-stale-route", IsPrimary: true, State: models.TaskSessionStateRunning},
		{ID: "force-primary-stale-target", TaskID: "force-primary-stale-route", State: models.TaskSessionStateCreated},
		{ID: "force-primary-terminal-current", TaskID: "force-primary-terminal", IsPrimary: true, State: models.TaskSessionStateRunning},
		{ID: "force-primary-terminal-target", TaskID: "force-primary-terminal", State: models.TaskSessionStateCompleted},
	} {
		require.NoError(t, repo.CreateTaskSession(ctx, session))
	}

	heldRoute := models.WorkflowSessionRoute{
		OperationID: "held-operation", DestinationStepID: "held-step", TargetKind: "session",
		DestinationID: "force-primary-held-target", Phase: "prepared",
	}
	require.NoError(t, repo.SetTaskMetadataKey(ctx, "force-primary-held", models.MetaKeyWorkflowSessionRoute, heldRoute))
	heldTask, err := repo.GetTask(ctx, "force-primary-held")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{
		TaskID: heldTask.ID, WorkspaceID: heldTask.WorkspaceID, TaskGeneration: heldTask.UpdatedAt,
		AdmissionGeneration: "admission", OperationID: "primary", RequestDigest: "request", PreviewDigest: "preview",
	})
	require.NoError(t, err)

	promoted, err := repo.SetSessionPrimaryWithWorkflowSessionRouteIfNonterminal(ctx, "force-primary-held-target", heldRoute)
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	require.False(t, promoted)
	current, err := repo.GetTaskSession(ctx, "force-primary-held-current")
	require.NoError(t, err)
	target, err := repo.GetTaskSession(ctx, "force-primary-held-target")
	require.NoError(t, err)
	require.True(t, current.IsPrimary)
	require.False(t, target.IsPrimary)
	storedHeldTask, err := repo.GetTask(ctx, "force-primary-held")
	require.NoError(t, err)
	storedRoute, ok := models.LoadWorkflowSessionRoute(storedHeldTask.Metadata)
	require.True(t, ok)
	require.Equal(t, "prepared", storedRoute.Phase)

	foreignRoute := models.WorkflowSessionRoute{
		OperationID: "foreign-operation", DestinationStepID: "foreign-step", TargetKind: "session",
		DestinationID: "force-primary-foreign-target", Phase: "prepared",
	}
	require.NoError(t, repo.SetTaskMetadataKey(ctx, "force-primary-foreign", models.MetaKeyWorkflowSessionRoute, foreignRoute))
	promoted, err = repo.SetSessionPrimaryWithWorkflowSessionRouteIfNonterminal(ctx, "force-primary-foreign-target", foreignRoute)
	require.NoError(t, err)
	require.True(t, promoted)
	foreignCurrent, err := repo.GetTaskSession(ctx, "force-primary-foreign-current")
	require.NoError(t, err)
	foreignTarget, err := repo.GetTaskSession(ctx, "force-primary-foreign-target")
	require.NoError(t, err)
	require.False(t, foreignCurrent.IsPrimary)
	require.True(t, foreignTarget.IsPrimary)
	foreignTask, err := repo.GetTask(ctx, "force-primary-foreign")
	require.NoError(t, err)
	committedRoute, ok := models.LoadWorkflowSessionRoute(foreignTask.Metadata)
	require.True(t, ok)
	require.Equal(t, "committed", committedRoute.Phase)

	staleRoute := models.WorkflowSessionRoute{
		OperationID: "stale-operation", DestinationStepID: "stale-step", TargetKind: "session",
		DestinationID: "force-primary-stale-target", Phase: "prepared",
	}
	require.NoError(t, repo.SetTaskMetadataKey(ctx, "force-primary-stale-route", models.MetaKeyWorkflowSessionRoute, staleRoute))
	staleRoute.OperationID = "superseded-operation"
	promoted, err = repo.SetSessionPrimaryWithWorkflowSessionRouteIfNonterminal(ctx, "force-primary-stale-target", staleRoute)
	require.ErrorContains(t, err, "workflow session route was superseded")
	require.False(t, promoted)
	staleCurrent, err := repo.GetTaskSession(ctx, "force-primary-stale-current")
	require.NoError(t, err)
	require.True(t, staleCurrent.IsPrimary)

	promoted, err = repo.SetSessionPrimaryIfNonterminal(ctx, "force-primary-terminal-target")
	require.NoError(t, err)
	require.False(t, promoted)
	terminalCurrent, err := repo.GetTaskSession(ctx, "force-primary-terminal-current")
	require.NoError(t, err)
	require.True(t, terminalCurrent.IsPrimary)
}
