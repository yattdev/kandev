package sqlite

import (
	"context"
	"testing"
	"time"

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

func TestClaimForceRemovalBlocksRecoveryCurrentTaskMetadata(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	now := time.Now().UTC()
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-recovery-ws", Name: "Force"}))
	for _, id := range []string{"held-recovery", "foreign-recovery"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: id, WorkspaceID: "force-recovery-ws", Title: id}))
	}
	for _, id := range []string{"held-recovery", "foreign-recovery"} {
		require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{ID: id + "-session", TaskID: id, State: models.TaskSessionStateWaitingForInput, UpdatedAt: now, Metadata: map[string]interface{}{models.SessionMetaKeyRecoverySettlementPending: models.InterruptedRecoverySettlement{Token: id + "-token"}}}))
	}
	held, err := repo.GetTask(ctx, "held-recovery")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: held.ID, WorkspaceID: held.WorkspaceID, TaskGeneration: held.UpdatedAt, AdmissionGeneration: "a", OperationID: "recovery-current", RequestDigest: "r", PreviewDigest: "p"})
	require.NoError(t, err)
	hs, err := repo.GetTaskSession(ctx, "held-recovery-session")
	require.NoError(t, err)
	written, err := repo.SetTaskMetadataKeyIfRecoveryCurrent(ctx, held.ID, hs.ID, hs.UpdatedAt, "held-recovery-token", "marker", true)
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	require.False(t, written)
	held, err = repo.GetTask(ctx, held.ID)
	require.NoError(t, err)
	require.NotContains(t, held.Metadata, "marker")
	fs, err := repo.GetTaskSession(ctx, "foreign-recovery-session")
	require.NoError(t, err)
	written, err = repo.SetTaskMetadataKeyIfRecoveryCurrent(ctx, "foreign-recovery", fs.ID, fs.UpdatedAt, "foreign-recovery-token", "marker", true)
	require.NoError(t, err)
	require.True(t, written)
}

func TestClaimForceRemovalBlocksSessionMetadataReplacement(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-session-metadata-ws", Name: "Force"}))
	for _, id := range []string{"held-session-task", "foreign-session-task"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: id, WorkspaceID: "force-session-metadata-ws", Title: id}))
		require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{ID: id + "-session", TaskID: id, Metadata: map[string]interface{}{"before": id}}))
	}
	held, err := repo.GetTask(ctx, "held-session-task")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: held.ID, WorkspaceID: held.WorkspaceID, TaskGeneration: held.UpdatedAt, AdmissionGeneration: "a", OperationID: "session-metadata", RequestDigest: "r", PreviewDigest: "p"})
	require.NoError(t, err)
	require.ErrorIs(t, repo.UpdateSessionMetadata(ctx, "held-session-task-session", map[string]interface{}{"after": "held"}), ErrForceRemovalTaskHeld)
	hs, err := repo.GetTaskSession(ctx, "held-session-task-session")
	require.NoError(t, err)
	require.Equal(t, "held-session-task", hs.Metadata["before"])
	require.NoError(t, repo.UpdateSessionMetadata(ctx, "foreign-session-task-session", map[string]interface{}{"after": "foreign"}))
	fs, err := repo.GetTaskSession(ctx, "foreign-session-task-session")
	require.NoError(t, err)
	require.Equal(t, "foreign", fs.Metadata["after"])
}

func TestClaimForceRemovalBlocksStateGuardedSessionMetadataWithoutPersistingIt(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-session-metadata-ws", Name: "Force"}))
	for _, taskID := range []string{"force-session-metadata-held", "force-session-metadata-foreign"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: "force-session-metadata-ws", Title: taskID}))
		require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{ID: taskID + "-session", TaskID: taskID, State: models.TaskSessionStateWaitingForInput}))
	}
	held, err := repo.GetTask(ctx, "force-session-metadata-held")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: held.ID, WorkspaceID: held.WorkspaceID, TaskGeneration: held.UpdatedAt, AdmissionGeneration: "a", OperationID: "session-metadata", RequestDigest: "r", PreviewDigest: "p"})
	require.NoError(t, err)

	written, err := repo.SetSessionMetadataKeyIfState(ctx, "force-session-metadata-held-session", "marker", true, models.TaskSessionStateWaitingForInput)
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	require.False(t, written)
	heldSession, err := repo.GetTaskSession(ctx, "force-session-metadata-held-session")
	require.NoError(t, err)
	require.NotContains(t, heldSession.Metadata, "marker")

	written, err = repo.SetSessionMetadataKeyIfState(ctx, "force-session-metadata-foreign-session", "marker", true, models.TaskSessionStateWaitingForInput)
	require.NoError(t, err)
	require.True(t, written)
}

func TestClaimForceRemovalBlocksSessionContextWindowWithoutPersistingIt(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-context-window-ws", Name: "Force"}))
	for _, taskID := range []string{"force-context-window-held", "force-context-window-foreign"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: "force-context-window-ws", Title: taskID}))
		require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{ID: taskID + "-session", TaskID: taskID}))
	}
	held, err := repo.GetTask(ctx, "force-context-window-held")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: held.ID, WorkspaceID: held.WorkspaceID, TaskGeneration: held.UpdatedAt, AdmissionGeneration: "a", OperationID: "context-window", RequestDigest: "r", PreviewDigest: "p"})
	require.NoError(t, err)

	count, err := repo.UpdateSessionContextWindow(ctx, "force-context-window-held-session", map[string]interface{}{"size": int64(200000), "used": int64(120000)})
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	require.Zero(t, count)
	heldSession, err := repo.GetTaskSession(ctx, "force-context-window-held-session")
	require.NoError(t, err)
	require.NotContains(t, heldSession.Metadata, models.SessionMetaKeyContextWindow)
	require.NotContains(t, heldSession.Metadata, models.SessionMetaKeyContextCompactionCount)

	count, err = repo.UpdateSessionContextWindow(ctx, "force-context-window-foreign-session", map[string]interface{}{"size": int64(200000), "used": int64(120000)})
	require.NoError(t, err)
	require.Zero(t, count)
}
