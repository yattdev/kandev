package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

func TestForceRemovalGrantConsumesOnceForExactLiveCallerAndTarget(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "grant-workspace", Name: "Grant"}))
	require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: "grant-target", WorkspaceID: "grant-workspace", Title: "Grant"}))
	task, err := repo.GetTask(ctx, "grant-target")
	require.NoError(t, err)
	grant, err := repo.IssueForceRemovalGrant(ctx, &models.ForceRemovalGrant{TaskID: task.ID, WorkspaceID: task.WorkspaceID, TaskGeneration: task.UpdatedAt, CallerTaskID: "caller", CallerSessionID: "session", IssuedByUserID: "admin", ExpiresAt: time.Now().Add(time.Minute)})
	require.NoError(t, err)
	require.NotEmpty(t, grant.ID)

	_, err = repo.ConsumeForceRemovalGrant(ctx, grant.ID, task.ID, task.WorkspaceID, "caller", "other-session", task.UpdatedAt)
	require.ErrorIs(t, err, ErrForceRemovalGrantUsed)
	consumed, err := repo.ConsumeForceRemovalGrant(ctx, grant.ID, task.ID, task.WorkspaceID, "caller", "session", task.UpdatedAt)
	require.NoError(t, err)
	require.NotNil(t, consumed.ConsumedAt)
	_, err = repo.ConsumeForceRemovalGrant(ctx, grant.ID, task.ID, task.WorkspaceID, "caller", "session", task.UpdatedAt)
	require.ErrorIs(t, err, ErrForceRemovalGrantUsed)
}
