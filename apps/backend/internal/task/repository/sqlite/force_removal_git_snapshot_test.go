package sqlite

import (
	"context"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestClaimForceRemovalBlocksLiveGitSnapshotUpsert(t *testing.T) {
	ctx := context.Background()
	r := newRepoForHealTests(t)
	require.NoError(t, r.CreateWorkspace(ctx, &models.Workspace{ID: "force-git-ws", Name: "Force"}))
	for _, id := range []string{"held", "foreign"} {
		require.NoError(t, r.CreateTask(ctx, &models.Task{ID: id, WorkspaceID: "force-git-ws", Title: id}))
		require.NoError(t, r.CreateTaskEnvironment(ctx, &models.TaskEnvironment{ID: id + "e", TaskID: id, ExecutorType: "local", Status: models.TaskEnvironmentStatusReady}))
	}
	h, e := r.GetTask(ctx, "held")
	require.NoError(t, e)
	_, _, e = r.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: h.ID, WorkspaceID: h.WorkspaceID, TaskGeneration: h.UpdatedAt, AdmissionGeneration: "a", OperationID: "o", RequestDigest: "r", PreviewDigest: "p"})
	require.NoError(t, e)
	require.ErrorIs(t, r.UpsertLatestLiveGitSnapshot(ctx, &models.GitSnapshot{ID: "heldsnap", TaskEnvironmentID: "helde"}), ErrForceRemovalTaskHeld)
	require.NoError(t, r.UpsertLatestLiveGitSnapshot(ctx, &models.GitSnapshot{ID: "foreignsnap", TaskEnvironmentID: "foreigne"}))
}

func TestClaimForceRemovalBlocksGitSnapshotCreateAcrossWritePaths(t *testing.T) {
	ctx := context.Background()
	r := newRepoForHealTests(t)
	require.NoError(t, r.CreateWorkspace(ctx, &models.Workspace{ID: "force-git-create-ws", Name: "Force"}))
	for _, id := range []string{"held", "foreign"} {
		require.NoError(t, r.CreateTask(ctx, &models.Task{ID: id, WorkspaceID: "force-git-create-ws", Title: id}))
		require.NoError(t, r.CreateTaskEnvironment(ctx, &models.TaskEnvironment{ID: id + "e", TaskID: id, ExecutorType: "local", Status: models.TaskEnvironmentStatusReady}))
	}
	held, err := r.GetTask(ctx, "held")
	require.NoError(t, err)
	_, _, err = r.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: held.ID, WorkspaceID: held.WorkspaceID, TaskGeneration: held.UpdatedAt, AdmissionGeneration: "a", OperationID: "o", RequestDigest: "r", PreviewDigest: "p"})
	require.NoError(t, err)

	for _, snapshot := range []*models.GitSnapshot{
		{ID: "held-live", TaskEnvironmentID: "helde", TriggeredBy: TriggeredByLiveMonitor},
		{ID: "held-complete", TaskEnvironmentID: "helde", TriggeredBy: triggeredByAgentCompleted},
	} {
		require.ErrorIs(t, r.CreateGitSnapshot(ctx, snapshot), ErrForceRemovalTaskHeld)
	}
	var heldCount int
	require.NoError(t, r.db.GetContext(ctx, &heldCount, `SELECT COUNT(*) FROM task_session_git_snapshots WHERE task_environment_id = ?`, "helde"))
	require.Zero(t, heldCount)

	require.NoError(t, r.CreateGitSnapshot(ctx, &models.GitSnapshot{ID: "foreign-complete", TaskEnvironmentID: "foreigne", TriggeredBy: triggeredByAgentCompleted}))
	var foreignCount int
	require.NoError(t, r.db.GetContext(ctx, &foreignCount, `SELECT COUNT(*) FROM task_session_git_snapshots WHERE task_environment_id = ?`, "foreigne"))
	require.Equal(t, 1, foreignCount)

	require.Error(t, r.CreateGitSnapshot(ctx, &models.GitSnapshot{ID: "missing", TaskEnvironmentID: "missing-environment", TriggeredBy: TriggeredByLiveMonitor}))
}
