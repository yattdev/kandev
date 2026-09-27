package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kandev/kandev/internal/task/models"
)

func TestClaimForceRemovalBlocksConditionalTaskMetadataWithoutPersistingIt(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-conditional-metadata-ws", Name: "Force"}))
	for _, taskID := range []string{"force-conditional-metadata-task", "force-conditional-metadata-stale", "force-conditional-metadata-foreign"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: "force-conditional-metadata-ws", Title: taskID}))
	}
	heldTask, err := repo.GetTask(ctx, "force-conditional-metadata-task")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: heldTask.ID, WorkspaceID: heldTask.WorkspaceID, TaskGeneration: heldTask.UpdatedAt, AdmissionGeneration: "admission", OperationID: "conditional-metadata-operation", RequestDigest: "request", PreviewDigest: "preview"})
	require.NoError(t, err)

	written, err := repo.SetTaskMetadataKeyIfNoActiveSession(ctx, heldTask.ID, "force_marker", true)
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	require.False(t, written)
	held, err := repo.GetTask(ctx, heldTask.ID)
	require.NoError(t, err)
	require.NotContains(t, held.Metadata, "force_marker")

	require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{ID: "force-conditional-metadata-stale-session", TaskID: "force-conditional-metadata-stale", State: models.TaskSessionStateStarting}))
	written, err = repo.SetTaskMetadataKeyIfNoActiveSession(ctx, "force-conditional-metadata-stale", "force_marker", true)
	require.NoError(t, err)
	require.False(t, written)
	stale, err := repo.GetTask(ctx, "force-conditional-metadata-stale")
	require.NoError(t, err)
	require.NotContains(t, stale.Metadata, "force_marker")

	written, err = repo.SetTaskMetadataKeyIfNoActiveSession(ctx, "force-conditional-metadata-foreign", "force_marker", true)
	require.NoError(t, err)
	require.True(t, written)
	foreign, err := repo.GetTask(ctx, "force-conditional-metadata-foreign")
	require.NoError(t, err)
	require.Equal(t, true, foreign.Metadata["force_marker"])
}

func TestClaimForceRemovalBlocksArchiveGuardedTaskMetadataWithoutPersistingIt(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-archive-metadata-ws", Name: "Force"}))
	for _, taskID := range []string{"force-archive-metadata-task", "force-archive-metadata-archived", "force-archive-metadata-foreign"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: "force-archive-metadata-ws", Title: taskID}))
	}
	heldTask, err := repo.GetTask(ctx, "force-archive-metadata-task")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: heldTask.ID, WorkspaceID: heldTask.WorkspaceID, TaskGeneration: heldTask.UpdatedAt, AdmissionGeneration: "admission", OperationID: "archive-metadata-operation", RequestDigest: "request", PreviewDigest: "preview"})
	require.NoError(t, err)

	written, err := repo.SetTaskMetadataKeyIfNotArchived(ctx, heldTask.ID, "force_marker", true)
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	require.False(t, written)
	held, err := repo.GetTask(ctx, heldTask.ID)
	require.NoError(t, err)
	require.NotContains(t, held.Metadata, "force_marker")

	require.NoError(t, repo.ArchiveTask(ctx, "force-archive-metadata-archived"))
	written, err = repo.SetTaskMetadataKeyIfNotArchived(ctx, "force-archive-metadata-archived", "force_marker", true)
	require.NoError(t, err)
	require.False(t, written)

	written, err = repo.SetTaskMetadataKeyIfNotArchived(ctx, "force-archive-metadata-foreign", "force_marker", true)
	require.NoError(t, err)
	require.True(t, written)
}

func TestClaimForceRemovalBlocksAbsentTaskMetadataWithoutPersistingIt(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-absent-metadata-ws", Name: "Force"}))
	for _, id := range []string{"held", "stale", "foreign"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: id, WorkspaceID: "force-absent-metadata-ws", Title: id}))
	}
	held, err := repo.GetTask(ctx, "held")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: held.ID, WorkspaceID: held.WorkspaceID, TaskGeneration: held.UpdatedAt, AdmissionGeneration: "a", OperationID: "absent-metadata", RequestDigest: "r", PreviewDigest: "p"})
	require.NoError(t, err)
	written, err := repo.SetTaskMetadataKeyIfAbsent(ctx, "held", "marker", true)
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	require.False(t, written)
	require.NoError(t, repo.SetTaskMetadataKey(ctx, "stale", "marker", true))
	written, err = repo.SetTaskMetadataKeyIfAbsent(ctx, "stale", "marker", false)
	require.NoError(t, err)
	require.False(t, written)
	written, err = repo.SetTaskMetadataKeyIfAbsent(ctx, "foreign", "marker", true)
	require.NoError(t, err)
	require.True(t, written)
}

func TestClaimForceRemovalBlocksPresentTaskMetadataWithoutPersistingIt(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-present-metadata-ws", Name: "Force"}))
	for _, id := range []string{"held-present", "stale-present", "foreign-present"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: id, WorkspaceID: "force-present-metadata-ws", Title: id}))
	}
	for _, id := range []string{"held-present", "foreign-present"} {
		require.NoError(t, repo.SetTaskMetadataKey(ctx, id, "marker", true))
	}
	held, err := repo.GetTask(ctx, "held-present")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: held.ID, WorkspaceID: held.WorkspaceID, TaskGeneration: held.UpdatedAt, AdmissionGeneration: "a", OperationID: "present-metadata", RequestDigest: "r", PreviewDigest: "p"})
	require.NoError(t, err)
	written, err := repo.SetTaskMetadataKeyIfPresent(ctx, held.ID, "marker", false)
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	require.False(t, written)
	written, err = repo.SetTaskMetadataKeyIfPresent(ctx, "stale-present", "marker", false)
	require.NoError(t, err)
	require.False(t, written)
	written, err = repo.SetTaskMetadataKeyIfPresent(ctx, "foreign-present", "marker", false)
	require.NoError(t, err)
	require.True(t, written)
}

func TestClaimForceRemovalBlocksAbsentLiveTaskMetadataWithoutPersistingIt(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-absent-live-ws", Name: "Force"}))
	for _, id := range []string{"held-live", "foreign-live"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: id, WorkspaceID: "force-absent-live-ws", Title: id}))
	}
	held, err := repo.GetTask(ctx, "held-live")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: held.ID, WorkspaceID: held.WorkspaceID, TaskGeneration: held.UpdatedAt, AdmissionGeneration: "a", OperationID: "absent-live", RequestDigest: "r", PreviewDigest: "p"})
	require.NoError(t, err)
	written, err := repo.SetTaskMetadataKeyIfAbsentNotArchived(ctx, held.ID, "marker", true)
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	require.False(t, written)
	written, err = repo.SetTaskMetadataKeyIfAbsentNotArchived(ctx, "foreign-live", "marker", true)
	require.NoError(t, err)
	require.True(t, written)
}
