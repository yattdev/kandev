package service

import (
	"context"
	"errors"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository"
)

type taskQueuePurgeOutcomeSpy struct {
	repository.TaskRepository
	outcomes   []bool
	operations []string
}

func (s *taskQueuePurgeOutcomeSpy) ResolveTaskQueuePurgeForOperation(_ context.Context, _, operationID string, committed bool) {
	s.outcomes = append(s.outcomes, committed)
	s.operations = append(s.operations, operationID)
}

func TestWorkspacePartialCancellationSettlesOnlyCancelledJob(t *testing.T) {
	ctx := context.Background()
	svc, _, repo := createTestService(t)
	first := &models.TaskResourceCleanupJob{
		ID: "partial-first", OperationID: "workspace-delete:first", TaskID: "partial-task-first",
		Trigger: models.TaskResourceCleanupTriggerWorkspaceDelete,
		State:   models.TaskResourceCleanupStatePrepared, ResourceSnapshot: `{}`,
	}
	second := &models.TaskResourceCleanupJob{
		ID: "partial-second", OperationID: "workspace-delete:second", TaskID: "partial-task-second",
		Trigger: models.TaskResourceCleanupTriggerWorkspaceDelete,
		State:   models.TaskResourceCleanupStateRunning, ResourceSnapshot: `{}`,
	}
	for _, job := range []*models.TaskResourceCleanupJob{first, second} {
		seedCleanupTaskAndSession(t, repo, job.TaskID, "session-"+job.TaskID)
		if err := repo.CreateTaskResourceCleanupJob(ctx, job); err != nil {
			t.Fatal(err)
		}
	}
	spy := &taskQueuePurgeOutcomeSpy{TaskRepository: repo}
	svc.tasks = spy
	err := svc.cancelWorkspaceDeleteTaskCleanupJobs(ctx, []workspaceDeleteTaskCleanup{
		{cleanupJob: first}, {cleanupJob: second},
	})
	if !errors.Is(err, ErrCleanupCancellationRace) {
		t.Fatalf("partial cancellation error = %v", err)
	}
	if len(spy.operations) != 1 || spy.operations[0] != first.OperationID || spy.outcomes[0] {
		t.Fatalf("resolved operations = %v outcomes = %v", spy.operations, spy.outcomes)
	}
	for _, tc := range []struct {
		job   *models.TaskResourceCleanupJob
		state models.TaskResourceCleanupState
	}{
		{first, models.TaskResourceCleanupStateCancelled},
		{second, models.TaskResourceCleanupStateRunning},
	} {
		current, err := repo.GetTaskResourceCleanupJob(ctx, tc.job.ID)
		if err != nil || current.State != tc.state {
			t.Fatalf("job %s state = %v, error = %v", tc.job.ID, current, err)
		}
	}
}

func TestKnownTaskMutationOutcomeSettlesQueueAfterDurableJobTransition(t *testing.T) {
	for _, tc := range []struct {
		name      string
		committed bool
	}{
		{name: "committed", committed: true},
		{name: "rolled_back"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			svc, _, repo := createTestService(t)
			taskID := "known-outcome-" + tc.name
			seedCleanupTaskAndSession(t, repo, taskID, "session-"+tc.name)
			job := &models.TaskResourceCleanupJob{
				ID: "job-" + tc.name, OperationID: "archive:" + tc.name,
				TaskID: taskID, Trigger: models.TaskResourceCleanupTriggerArchive,
				State: models.TaskResourceCleanupStatePrepared, ResourceSnapshot: `{}`,
			}
			if err := repo.CreateTaskResourceCleanupJob(ctx, job); err != nil {
				t.Fatal(err)
			}
			if tc.committed {
				if err := repo.ArchiveTask(ctx, taskID); err != nil {
					t.Fatal(err)
				}
			}
			spy := &taskQueuePurgeOutcomeSpy{TaskRepository: repo}
			svc.tasks = spy
			svc.resolveTaskResourceCleanupAfterMutationError(ctx, job)
			current, err := repo.GetTaskResourceCleanupJob(ctx, job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if tc.committed && current.State == models.TaskResourceCleanupStatePrepared {
				t.Fatalf("committed job remained prepared when purge settled")
			}
			if !tc.committed && current.State != models.TaskResourceCleanupStateCancelled {
				t.Fatalf("rolled-back job state = %s, want cancelled", current.State)
			}
			if len(spy.outcomes) != 1 || spy.outcomes[0] != tc.committed {
				t.Fatalf("queue purge outcomes = %v, want [%t]", spy.outcomes, tc.committed)
			}
		})
	}
}
