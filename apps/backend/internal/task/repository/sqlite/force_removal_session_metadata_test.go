package sqlite

import (
	"context"
	"testing"
	"time"

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

func TestClaimForceRemovalBlocksCurrentSessionStateTransition(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-state-ws", Name: "Force"}))
	for _, taskID := range []string{"force-state-held", "force-state-foreign"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: "force-state-ws", Title: taskID}))
		require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{
			ID: taskID + "-session", TaskID: taskID, State: models.TaskSessionStateCreated,
		}))
	}
	held, err := repo.GetTask(ctx, "force-state-held")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{
		TaskID: held.ID, WorkspaceID: held.WorkspaceID, TaskGeneration: held.UpdatedAt,
		AdmissionGeneration: "admission", OperationID: "session-state-operation",
		RequestDigest: "request", PreviewDigest: "preview",
	})
	require.NoError(t, err)

	changed, _, err := repo.UpdateTaskSessionStateIfCurrent(
		ctx, "force-state-held-session", models.TaskSessionStateCreated, models.TaskSessionStateRunning, "",
	)
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	require.False(t, changed)
	heldSession, err := repo.GetTaskSession(ctx, "force-state-held-session")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateCreated, heldSession.State)

	changed, _, err = repo.UpdateTaskSessionStateIfCurrent(
		ctx, "force-state-foreign-session", models.TaskSessionStateCreated, models.TaskSessionStateRunning, "",
	)
	require.NoError(t, err)
	require.True(t, changed)
	changed, _, err = repo.UpdateTaskSessionStateIfCurrent(
		ctx, "force-state-foreign-session", models.TaskSessionStateCreated, models.TaskSessionStateCompleted, "stale",
	)
	require.NoError(t, err)
	require.False(t, changed)
	foreignSession, err := repo.GetTaskSession(ctx, "force-state-foreign-session")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateRunning, foreignSession.State)
}

func TestClaimForceRemovalBlocksIdentitySessionStateTransition(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-identity-ws", Name: "Force"}))
	for _, taskID := range []string{"force-identity-held", "force-identity-foreign"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: "force-identity-ws", Title: taskID}))
		require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{ID: taskID + "-session", TaskID: taskID, QueueIncarnationID: "current", State: models.TaskSessionStateCreated}))
	}
	held, err := repo.GetTask(ctx, "force-identity-held")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: held.ID, WorkspaceID: held.WorkspaceID, TaskGeneration: held.UpdatedAt, AdmissionGeneration: "admission", OperationID: "identity-state", RequestDigest: "request", PreviewDigest: "preview"})
	require.NoError(t, err)
	changed, _, err := repo.UpdateTaskSessionStateIfCurrentIdentity(ctx, held.ID, "force-identity-held-session", "current", models.TaskSessionStateCreated, models.TaskSessionStateRunning, "")
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	require.False(t, changed)
	changed, _, err = repo.UpdateTaskSessionStateIfCurrentIdentity(ctx, "force-identity-foreign", "force-identity-foreign-session", "current", models.TaskSessionStateCreated, models.TaskSessionStateRunning, "")
	require.NoError(t, err)
	require.True(t, changed)
	changed, _, err = repo.UpdateTaskSessionStateIfCurrentIdentity(ctx, "force-identity-foreign", "force-identity-foreign-session", "stale", models.TaskSessionStateRunning, models.TaskSessionStateCompleted, "")
	require.NoError(t, err)
	require.False(t, changed)
}

func TestClaimForceRemovalBlocksActiveSessionCancellation(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-cancel-ws", Name: "Force"}))
	for _, taskID := range []string{"force-cancel-held", "force-cancel-foreign", "force-cancel-stale"} {
		state := models.TaskSessionStateRunning
		if taskID == "force-cancel-stale" {
			state = models.TaskSessionStateCompleted
		}
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: "force-cancel-ws", Title: taskID}))
		require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{ID: taskID + "-session", TaskID: taskID, State: state}))
	}
	held, err := repo.GetTask(ctx, "force-cancel-held")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: held.ID, WorkspaceID: held.WorkspaceID, TaskGeneration: held.UpdatedAt, AdmissionGeneration: "admission", OperationID: "cancel", RequestDigest: "request", PreviewDigest: "preview"})
	require.NoError(t, err)
	changed, _, err := repo.CancelActiveTaskSession(ctx, "force-cancel-held-session", "held")
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	require.False(t, changed)
	changed, _, err = repo.CancelActiveTaskSession(ctx, "force-cancel-foreign-session", "foreign")
	require.NoError(t, err)
	require.True(t, changed)
	changed, _, err = repo.CancelActiveTaskSession(ctx, "force-cancel-stale-session", "stale")
	require.NoError(t, err)
	require.False(t, changed)
}

func TestClaimForceRemovalBlocksBulkActiveSessionCancellation(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-bulk-cancel-ws", Name: "Force"}))
	for _, taskID := range []string{"force-bulk-held", "force-bulk-foreign"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: "force-bulk-cancel-ws", Title: taskID}))
		require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{ID: taskID + "-session", TaskID: taskID, State: models.TaskSessionStateRunning}))
	}
	held, err := repo.GetTask(ctx, "force-bulk-held")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: held.ID, WorkspaceID: held.WorkspaceID, TaskGeneration: held.UpdatedAt, AdmissionGeneration: "admission", OperationID: "bulk-cancel", RequestDigest: "request", PreviewDigest: "preview"})
	require.NoError(t, err)
	_, err = repo.CancelActiveTaskSessionsByTaskID(ctx, held.ID, "held")
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	hs, err := repo.GetTaskSession(ctx, "force-bulk-held-session")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateRunning, hs.State)
	foreign, err := repo.CancelActiveTaskSessionsByTaskID(ctx, "force-bulk-foreign", "foreign")
	require.NoError(t, err)
	require.Len(t, foreign, 1)
}

func TestClaimForceRemovalBlocksSelectedActiveSessionCancellation(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-selected-cancel-ws", Name: "Force"}))
	for _, taskID := range []string{"force-selected-held", "force-selected-foreign"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: "force-selected-cancel-ws", Title: taskID}))
		require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{ID: taskID + "-running", TaskID: taskID, State: models.TaskSessionStateRunning}))
	}
	require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{ID: "force-selected-foreign-completed", TaskID: "force-selected-foreign", State: models.TaskSessionStateCompleted}))

	held, err := repo.GetTask(ctx, "force-selected-held")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: held.ID, WorkspaceID: held.WorkspaceID, TaskGeneration: held.UpdatedAt, AdmissionGeneration: "admission", OperationID: "selected-cancel", RequestDigest: "request", PreviewDigest: "preview"})
	require.NoError(t, err)

	_, err = repo.CancelActiveTaskSessionsByIDs(ctx, held.ID, []string{"force-selected-held-running"}, "held")
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	heldSession, err := repo.GetTaskSession(ctx, "force-selected-held-running")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateRunning, heldSession.State)

	foreign, err := repo.CancelActiveTaskSessionsByIDs(ctx, "force-selected-foreign", []string{"force-selected-foreign-running", "force-selected-foreign-completed"}, "foreign")
	require.NoError(t, err)
	require.Len(t, foreign, 1)
	require.Equal(t, "force-selected-foreign-running", foreign[0].ID)
	completed, err := repo.GetTaskSession(ctx, "force-selected-foreign-completed")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateCompleted, completed.State)
}

func TestClaimForceRemovalBlocksStaleRunningSessionCancellation(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-stale-cancel-ws", Name: "Force"}))
	for _, taskID := range []string{"force-stale-cancel-held", "force-stale-cancel-foreign", "force-stale-cancel-terminal"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: "force-stale-cancel-ws", Title: taskID}))
		state := models.TaskSessionStateRunning
		if taskID == "force-stale-cancel-terminal" {
			state = models.TaskSessionStateCompleted
		}
		require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{ID: taskID + "-session", TaskID: taskID, State: state}))
	}

	held, err := repo.GetTask(ctx, "force-stale-cancel-held")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: held.ID, WorkspaceID: held.WorkspaceID, TaskGeneration: held.UpdatedAt, AdmissionGeneration: "admission", OperationID: "stale-cancel", RequestDigest: "request", PreviewDigest: "preview"})
	require.NoError(t, err)

	staleBefore := time.Now().UTC().Add(time.Minute)
	cancelled, err := repo.CancelRunningTaskSessionByID(ctx, "force-stale-cancel-held-session", "held", staleBefore)
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	require.Nil(t, cancelled)
	heldSession, err := repo.GetTaskSession(ctx, "force-stale-cancel-held-session")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateRunning, heldSession.State)

	cancelled, err = repo.CancelRunningTaskSessionByID(ctx, "force-stale-cancel-foreign-session", "foreign", staleBefore)
	require.NoError(t, err)
	require.NotNil(t, cancelled)
	require.Equal(t, models.TaskSessionStateCancelled, cancelled.State)

	cancelled, err = repo.CancelRunningTaskSessionByID(ctx, "force-stale-cancel-terminal-session", "terminal", staleBefore)
	require.NoError(t, err)
	require.Nil(t, cancelled)
}

func TestClaimForceRemovalBlocksLastAgentErrorDismissal(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-dismiss-error-ws", Name: "Force"}))
	lastErr := models.LastAgentError{Message: "connection lost", OccurredAt: time.Date(2026, 9, 28, 4, 0, 0, 0, time.UTC)}
	for _, taskID := range []string{"force-dismiss-error-held", "force-dismiss-error-foreign"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: "force-dismiss-error-ws", Title: taskID}))
		require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{ID: taskID + "-session", TaskID: taskID, Metadata: map[string]interface{}{models.SessionMetaKeyLastAgentError: lastErr}}))
	}
	held, err := repo.GetTask(ctx, "force-dismiss-error-held")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: held.ID, WorkspaceID: held.WorkspaceID, TaskGeneration: held.UpdatedAt, AdmissionGeneration: "admission", OperationID: "dismiss-error", RequestDigest: "request", PreviewDigest: "preview"})
	require.NoError(t, err)

	dismissed, err := repo.DismissLastAgentError(ctx, "force-dismiss-error-held-session", lastErr, time.Now().UTC())
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	require.False(t, dismissed)
	heldSession, err := repo.GetTaskSession(ctx, "force-dismiss-error-held-session")
	require.NoError(t, err)
	stored, ok := models.LoadLastAgentError(heldSession.Metadata)
	require.True(t, ok)
	require.False(t, stored.IsDismissed())

	dismissed, err = repo.DismissLastAgentError(ctx, "force-dismiss-error-foreign-session", lastErr, time.Now().UTC())
	require.NoError(t, err)
	require.True(t, dismissed)
}

func TestClaimForceRemovalBlocksACPSessionIDWithoutPersistingIt(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-acp-session-ws", Name: "Force"}))
	for _, taskID := range []string{"force-acp-session-held", "force-acp-session-foreign"} {
		sessionID := taskID + "-session"
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: "force-acp-session-ws", Title: taskID}))
		require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{ID: sessionID, TaskID: taskID}))
		require.NoError(t, repo.UpsertExecutorRunning(ctx, &models.ExecutorRunning{SessionID: sessionID, TaskID: taskID, AgentExecutionID: taskID + "-execution", ResumeToken: taskID + "-acp"}))
	}
	held, err := repo.GetTask(ctx, "force-acp-session-held")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: held.ID, WorkspaceID: held.WorkspaceID, TaskGeneration: held.UpdatedAt, AdmissionGeneration: "admission", OperationID: "acp-session", RequestDigest: "request", PreviewDigest: "preview"})
	require.NoError(t, err)

	written, err := repo.SetSessionACPSessionID(ctx, "force-acp-session-held-session", "force-acp-session-held-acp")
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	require.False(t, written)
	heldSession, err := repo.GetTaskSession(ctx, "force-acp-session-held-session")
	require.NoError(t, err)
	require.NotContains(t, heldSession.Metadata, "acp")

	written, err = repo.SetSessionACPSessionID(ctx, "force-acp-session-foreign-session", "force-acp-session-foreign-acp")
	require.NoError(t, err)
	require.True(t, written)
	written, err = repo.SetSessionACPSessionID(ctx, "force-acp-session-foreign-session", "force-acp-session-foreign-acp")
	require.NoError(t, err)
	require.False(t, written)
}

func TestClaimForceRemovalBlocksAbsentStateSessionMetadataWithoutPersistingIt(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-absent-state-ws", Name: "Force"}))
	for _, taskID := range []string{"force-absent-state-held", "force-absent-state-foreign", "force-absent-state-terminal"} {
		state := models.TaskSessionStateCreated
		if taskID == "force-absent-state-terminal" {
			state = models.TaskSessionStateCompleted
		}
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: "force-absent-state-ws", Title: taskID}))
		require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{ID: taskID + "-session", TaskID: taskID, State: state}))
	}
	held, err := repo.GetTask(ctx, "force-absent-state-held")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: held.ID, WorkspaceID: held.WorkspaceID, TaskGeneration: held.UpdatedAt, AdmissionGeneration: "admission", OperationID: "absent-state", RequestDigest: "request", PreviewDigest: "preview"})
	require.NoError(t, err)

	written, err := repo.SetSessionMetadataKeyIfAbsentIfState(ctx, "force-absent-state-held-session", "marker", true, models.TaskSessionStateCreated)
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	require.False(t, written)
	heldSession, err := repo.GetTaskSession(ctx, "force-absent-state-held-session")
	require.NoError(t, err)
	require.NotContains(t, heldSession.Metadata, "marker")

	written, err = repo.SetSessionMetadataKeyIfAbsentIfState(ctx, "force-absent-state-foreign-session", "marker", true, models.TaskSessionStateCreated)
	require.NoError(t, err)
	require.True(t, written)
	written, err = repo.SetSessionMetadataKeyIfAbsentIfState(ctx, "force-absent-state-foreign-session", "marker", false, models.TaskSessionStateCreated)
	require.NoError(t, err)
	require.False(t, written)
	written, err = repo.SetSessionMetadataKeyIfAbsentIfState(ctx, "force-absent-state-terminal-session", "marker", true, models.TaskSessionStateCreated)
	require.NoError(t, err)
	require.False(t, written)
}

func TestClaimForceRemovalBlocksStateGuardedSessionMetadataRemoval(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-remove-state-ws", Name: "Force"}))
	for _, taskID := range []string{"force-remove-state-held", "force-remove-state-foreign", "force-remove-state-terminal"} {
		state := models.TaskSessionStateCreated
		if taskID == "force-remove-state-terminal" {
			state = models.TaskSessionStateCompleted
		}
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: "force-remove-state-ws", Title: taskID}))
		require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{ID: taskID + "-session", TaskID: taskID, State: state, Metadata: map[string]interface{}{"marker": true}}))
	}
	held, err := repo.GetTask(ctx, "force-remove-state-held")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: held.ID, WorkspaceID: held.WorkspaceID, TaskGeneration: held.UpdatedAt, AdmissionGeneration: "admission", OperationID: "remove-state", RequestDigest: "request", PreviewDigest: "preview"})
	require.NoError(t, err)

	removed, err := repo.RemoveSessionMetadataKeyIfState(ctx, "force-remove-state-held-session", "marker", models.TaskSessionStateCreated)
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	require.False(t, removed)
	heldSession, err := repo.GetTaskSession(ctx, "force-remove-state-held-session")
	require.NoError(t, err)
	require.Contains(t, heldSession.Metadata, "marker")

	removed, err = repo.RemoveSessionMetadataKeyIfState(ctx, "force-remove-state-foreign-session", "marker", models.TaskSessionStateCreated)
	require.NoError(t, err)
	require.True(t, removed)
	removed, err = repo.RemoveSessionMetadataKeyIfState(ctx, "force-remove-state-terminal-session", "marker", models.TaskSessionStateCreated)
	require.NoError(t, err)
	require.False(t, removed)
}
