package service

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository"
)

type taskQueuePurgeOutcomeSpy struct {
	repository.TaskRepository
	outcomes []bool
}

func (s *taskQueuePurgeOutcomeSpy) ResolveTaskQueuePurge(_ context.Context, _ string, committed bool) {
	s.outcomes = append(s.outcomes, committed)
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
