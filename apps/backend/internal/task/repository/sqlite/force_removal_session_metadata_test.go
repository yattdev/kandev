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

func TestClaimForceRemovalBlocksAbsentSessionMetadataKey(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{
		ID: "force-session-absent-ws", Name: "Force",
	}))
	for _, taskID := range []string{"force-session-absent-held", "force-session-absent-foreign"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{
			ID: taskID, WorkspaceID: "force-session-absent-ws", Title: taskID,
		}))
		require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{
			ID: taskID + "-session", TaskID: taskID,
		}))
	}

	held, err := repo.GetTask(ctx, "force-session-absent-held")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{
		TaskID:              held.ID,
		WorkspaceID:         held.WorkspaceID,
		TaskGeneration:      held.UpdatedAt,
		AdmissionGeneration: "admission",
		OperationID:         "session-absent-operation",
		RequestDigest:       "request",
		PreviewDigest:       "preview",
	})
	require.NoError(t, err)

	written, err := repo.SetSessionMetadataKeyIfAbsent(
		ctx, "force-session-absent-held-session", "marker", "held",
	)
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	require.False(t, written)
	heldSession, err := repo.GetTaskSession(ctx, "force-session-absent-held-session")
	require.NoError(t, err)
	require.NotContains(t, heldSession.Metadata, "marker")

	written, err = repo.SetSessionMetadataKeyIfAbsent(
		ctx, "force-session-absent-foreign-session", "marker", "foreign",
	)
	require.NoError(t, err)
	require.True(t, written)
	written, err = repo.SetSessionMetadataKeyIfAbsent(
		ctx, "force-session-absent-foreign-session", "marker", "replacement",
	)
	require.NoError(t, err)
	require.False(t, written)
	foreignSession, err := repo.GetTaskSession(ctx, "force-session-absent-foreign-session")
	require.NoError(t, err)
	require.Equal(t, "foreign", foreignSession.Metadata["marker"])
}

func TestClaimForceRemovalBlocksDifferentStepSessionMetadataKey(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{
		ID: "force-session-step-ws", Name: "Force",
	}))
	for _, taskID := range []string{"force-session-step-held", "force-session-step-foreign"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{
			ID: taskID, WorkspaceID: "force-session-step-ws", Title: taskID,
		}))
		require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{
			ID: taskID + "-session", TaskID: taskID,
		}))
	}

	held, err := repo.GetTask(ctx, "force-session-step-held")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{
		TaskID: held.ID, WorkspaceID: held.WorkspaceID, TaskGeneration: held.UpdatedAt,
		AdmissionGeneration: "admission", OperationID: "session-step-operation",
		RequestDigest: "request", PreviewDigest: "preview",
	})
	require.NoError(t, err)

	written, err := repo.SetSessionMetadataKeyIfAbsentOrDifferentStep(
		ctx, "force-session-step-held-session", "marker", "step-held", map[string]string{"step_id": "step-held"},
	)
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	require.False(t, written)
	heldSession, err := repo.GetTaskSession(ctx, "force-session-step-held-session")
	require.NoError(t, err)
	require.NotContains(t, heldSession.Metadata, "marker")

	written, err = repo.SetSessionMetadataKeyIfAbsentOrDifferentStep(
		ctx, "force-session-step-foreign-session", "marker", "step-foreign", map[string]string{"step_id": "step-foreign"},
	)
	require.NoError(t, err)
	require.True(t, written)
	written, err = repo.SetSessionMetadataKeyIfAbsentOrDifferentStep(
		ctx, "force-session-step-foreign-session", "marker", "step-foreign", map[string]string{"step_id": "step-foreign", "replacement": "yes"},
	)
	require.NoError(t, err)
	require.False(t, written)
	foreignSession, err := repo.GetTaskSession(ctx, "force-session-step-foreign-session")
	require.NoError(t, err)
	marker, ok := foreignSession.Metadata["marker"].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, "step-foreign", marker["step_id"])
	require.NotContains(t, marker, "replacement")
}
