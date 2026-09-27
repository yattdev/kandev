package sqlite

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/kandev/kandev/internal/task/models"
)

// @covers AC-TASKS-SAFE-FORCE-REMOVAL-004.1
// @covers AC-TASKS-SAFE-FORCE-REMOVAL-004.2
func TestClaimForceRemovalRejectsStaleOrForeignTaskAndHoldsCleanup(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-ws", Name: "Force"}))
	require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: "force-task", WorkspaceID: "force-ws", Title: "Force"}))
	task, err := repo.GetTask(ctx, "force-task")
	require.NoError(t, err)

	claim := &models.ForceRemovalClaim{TaskID: task.ID, WorkspaceID: task.WorkspaceID, TaskGeneration: task.UpdatedAt, AdmissionGeneration: "admission", OperationID: "operation", RequestDigest: "request", PreviewDigest: "preview"}
	stored, replay, err := repo.ClaimForceRemoval(ctx, claim)
	require.NoError(t, err)
	require.False(t, replay)
	require.Equal(t, claim, stored)
	require.NoError(t, repo.AppendForceRemovalReceipt(ctx, claim.OperationID, models.ExactRetirementPredicateReceipt{Predicate: models.ExactRetirementIdentityPredicate, Status: models.ExactRetirementReceiptPass, ReasonCode: "EXACT_TASK_CLAIMED", ResourceID: task.ID, ObservedGeneration: task.UpdatedAt.UTC().Format(time.RFC3339Nano), EvidenceDigest: "digest"}))
	receipts, err := repo.ListForceRemovalReceipts(ctx, claim.OperationID)
	require.NoError(t, err)
	require.Len(t, receipts, 1)
	require.Equal(t, "EXACT_TASK_CLAIMED", receipts[0].ReasonCode)

	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: task.ID, WorkspaceID: "foreign", TaskGeneration: task.UpdatedAt, AdmissionGeneration: "admission", OperationID: "other", RequestDigest: "request", PreviewDigest: "preview"})
	require.ErrorIs(t, err, ErrForceRemovalClaimStale)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: task.ID, WorkspaceID: task.WorkspaceID, TaskGeneration: task.UpdatedAt, AdmissionGeneration: "admission", OperationID: "operation", RequestDigest: "different", PreviewDigest: "preview"})
	require.ErrorIs(t, err, ErrForceRemovalClaimConflict)

	err = repo.CreateTaskResourceCleanupJob(ctx, &models.TaskResourceCleanupJob{TaskID: task.ID, OperationID: "cleanup", Trigger: models.TaskResourceCleanupTriggerDelete, ResourceSnapshot: `{}`})
	require.ErrorIs(t, err, ErrForceRemovalCleanupHeld)
	jobs, err := repo.ListTaskResourceCleanupJobs(ctx, task.ID)
	require.NoError(t, err)
	require.Empty(t, jobs)

	err = repo.CreateTaskSession(ctx, &models.TaskSession{ID: "force-session", TaskID: task.ID})
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	sessions, err := repo.ListTaskSessions(ctx, task.ID)
	require.NoError(t, err)
	require.Empty(t, sessions)

	err = repo.CreateTaskSessionWithWorkspaceBinding(ctx, &models.TaskSession{ID: "force-worktree-session", TaskID: task.ID}, &models.TaskEnvironment{ID: "force-environment", TaskID: task.ID})
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	environment, err := repo.GetTaskEnvironmentByTaskID(ctx, task.ID)
	require.NoError(t, err)
	require.Nil(t, environment)
}

// @covers AC-TASKS-SAFE-FORCE-REMOVAL-004.1
func TestClaimForceRemovalOperationCannotBeReusedForAnotherTask(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-op-ws", Name: "Force"}))
	for _, taskID := range []string{"force-op-first", "force-op-second"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: "force-op-ws", Title: taskID}))
	}
	first, err := repo.GetTask(ctx, "force-op-first")
	require.NoError(t, err)
	second, err := repo.GetTask(ctx, "force-op-second")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: first.ID, WorkspaceID: first.WorkspaceID, TaskGeneration: first.UpdatedAt, AdmissionGeneration: "admission", OperationID: "shared-operation", RequestDigest: "request", PreviewDigest: "preview"})
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: second.ID, WorkspaceID: second.WorkspaceID, TaskGeneration: second.UpdatedAt, AdmissionGeneration: "admission", OperationID: "shared-operation", RequestDigest: "request", PreviewDigest: "preview"})
	require.ErrorIs(t, err, ErrForceRemovalClaimConflict)
}

// @covers AC-TASKS-SAFE-FORCE-REMOVAL-004.1
func TestClaimForceRemovalConcurrentExactRequestReplays(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-race-ws", Name: "Force"}))
	require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: "force-race-task", WorkspaceID: "force-race-ws", Title: "Force"}))
	task, err := repo.GetTask(ctx, "force-race-task")
	require.NoError(t, err)

	claim := models.ForceRemovalClaim{TaskID: task.ID, WorkspaceID: task.WorkspaceID, TaskGeneration: task.UpdatedAt, AdmissionGeneration: "admission", OperationID: "operation", RequestDigest: "request", PreviewDigest: "preview"}
	start := make(chan struct{})
	results := make(chan bool, 2)
	errs := make(chan error, 2)
	var callers sync.WaitGroup
	for range 2 {
		callers.Add(1)
		go func() {
			defer callers.Done()
			<-start
			attempt := claim
			_, replay, err := repo.ClaimForceRemoval(ctx, &attempt)
			if err != nil {
				errs <- err
				return
			}
			results <- replay
		}()
	}
	close(start)
	callers.Wait()
	close(results)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	var replays int
	for replay := range results {
		if replay {
			replays++
		}
	}
	require.Equal(t, 1, replays)
}
