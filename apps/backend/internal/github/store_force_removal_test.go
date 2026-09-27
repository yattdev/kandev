package github

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/kandev/kandev/internal/task/models"
)

func TestStorePRWatchWritesRequireForceRemovalClaimSchemaAndRespectClaims(t *testing.T) {
	_, _, _, store := setupPollerTest(t)
	ctx := context.Background()

	var present bool
	require.NoError(t, store.db.Get(&present, `SELECT EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'task_force_removal_claims')`))
	require.True(t, present)

	for _, taskID := range []string{"force-pr-watch-task", "force-pr-watch-foreign"} {
		seedTask(t, store, taskID, false)
	}
	now := time.Now().UTC()
	_, err := store.db.Exec(`INSERT INTO task_force_removal_claims (task_id, workspace_id, task_generation, admission_generation, operation_id, request_digest, preview_digest, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"force-pr-watch-task", testWorkspaceID, now, "admission", "force-pr-watch-operation", "request", "preview", now, now)
	require.NoError(t, err)

	err = store.CreatePRWatch(ctx, withTestWorkspace(&PRWatch{SessionID: "force-pr-watch-session", TaskID: "force-pr-watch-task", Owner: "owner", Repo: "repo", Branch: "feature/held"}))
	require.ErrorIs(t, err, models.ErrForceRemovalTaskHeld)
	watch, err := store.GetPRWatchByTaskRepoBranch(ctx, "force-pr-watch-task", "", "feature/held")
	require.NoError(t, err)
	require.Nil(t, watch)

	require.NoError(t, store.CreatePRWatch(ctx, withTestWorkspace(&PRWatch{SessionID: "force-pr-watch-foreign-session", TaskID: "force-pr-watch-foreign", Owner: "owner", Repo: "repo", Branch: "feature/allowed"})))
	watch, err = store.GetPRWatchByTaskRepoBranch(ctx, "force-pr-watch-foreign", "", "feature/allowed")
	require.NoError(t, err)
	require.NotNil(t, watch)
}

func TestStorePRWatchMutationDoesNotChangeClaimedWatch(t *testing.T) {
	_, _, _, store := setupPollerTest(t)
	ctx := context.Background()
	seedTask(t, store, "force-pr-watch-existing", false)
	watch := withTestWorkspace(&PRWatch{SessionID: "force-pr-watch-existing-session", TaskID: "force-pr-watch-existing", Owner: "owner", Repo: "repo", Branch: "feature/retained"})
	require.NoError(t, store.CreatePRWatch(ctx, watch))
	now := time.Now().UTC()
	_, err := store.db.Exec(`INSERT INTO task_force_removal_claims (task_id, workspace_id, task_generation, admission_generation, operation_id, request_digest, preview_digest, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		watch.TaskID, testWorkspaceID, now, "admission", "force-pr-watch-existing-operation", "request", "preview", now, now)
	require.NoError(t, err)

	require.ErrorIs(t, store.UpdatePRWatchTimestamps(ctx, watch.ID, now, nil, "checked", "reviewed"), models.ErrForceRemovalTaskHeld)
	require.ErrorIs(t, store.DeletePRWatch(ctx, watch.ID), models.ErrForceRemovalTaskHeld)
	_, err = store.DeletePRWatchesByTaskID(ctx, watch.TaskID)
	require.ErrorIs(t, err, models.ErrForceRemovalTaskHeld)
	stored, err := store.GetPRWatch(ctx, watch.ID)
	require.NoError(t, err)
	require.NotNil(t, stored)
	require.Empty(t, stored.LastCheckStatus)
}
