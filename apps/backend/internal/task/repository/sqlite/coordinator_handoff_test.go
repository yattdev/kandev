package sqlite

import (
	"context"
	"errors"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
)

func TestPromoteCoordinatorSuccessorFencesOnlyExpectedPrimary(t *testing.T) {
	repo := newRepoForSessionTests(t)
	ctx := context.Background()
	const taskID = "coordinator-task"
	requireCoordinatorHandoffNoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, Title: "Coordinator"}))
	requireCoordinatorHandoffNoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{
		ID: "predecessor", TaskID: taskID, IsPrimary: true, State: models.TaskSessionStateIdle,
	}))
	requireCoordinatorHandoffNoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{
		ID: "successor", TaskID: taskID, State: models.TaskSessionStateWaitingForInput,
	}))
	predecessor, err := repo.GetTaskSession(ctx, "predecessor")
	requireCoordinatorHandoffNoError(t, err)
	successor, err := repo.GetTaskSession(ctx, "successor")
	requireCoordinatorHandoffNoError(t, err)

	changed, err := repo.PromoteCoordinatorSuccessor(
		ctx, taskID, predecessor.ID, successor.ID,
		predecessor.QueueIncarnationID, successor.QueueIncarnationID, "operation-1",
	)
	requireCoordinatorHandoffNoError(t, err)
	if !changed {
		t.Fatal("promotion did not report a change")
	}
	predecessor, err = repo.GetTaskSession(ctx, predecessor.ID)
	requireCoordinatorHandoffNoError(t, err)
	successor, err = repo.GetTaskSession(ctx, successor.ID)
	requireCoordinatorHandoffNoError(t, err)
	if predecessor.IsPrimary || predecessor.RouteState != models.TaskSessionRouteStateCoordinatorHandoffFenced || predecessor.RouteReason != "operation-1" {
		t.Fatalf("predecessor after promotion = %+v, want fenced non-primary", predecessor)
	}
	if !successor.IsPrimary {
		t.Fatal("successor was not promoted")
	}

	changed, err = repo.PromoteCoordinatorSuccessor(
		ctx, taskID, predecessor.ID, successor.ID,
		predecessor.QueueIncarnationID, successor.QueueIncarnationID, "operation-1",
	)
	requireNoError(t, err)
	if changed {
		t.Fatal("identical retry changed the handoff")
	}
	_, err = repo.PromoteCoordinatorSuccessor(
		ctx, taskID, predecessor.ID, successor.ID,
		predecessor.QueueIncarnationID, successor.QueueIncarnationID, "operation-2",
	)
	if !errors.Is(err, models.ErrCoordinatorHandoffConflict) {
		t.Fatalf("conflicting retry error = %v, want conflict", err)
	}
	requireCoordinatorHandoffNoError(t, repo.RollbackCoordinatorSuccessor(
		ctx, taskID, predecessor.ID, successor.ID,
		predecessor.QueueIncarnationID, successor.QueueIncarnationID, "operation-1",
	))
	predecessor, err = repo.GetTaskSession(ctx, predecessor.ID)
	requireCoordinatorHandoffNoError(t, err)
	successor, err = repo.GetTaskSession(ctx, successor.ID)
	requireCoordinatorHandoffNoError(t, err)
	if !predecessor.IsPrimary || predecessor.RouteState != "" || successor.IsPrimary {
		t.Fatalf("rollback state predecessor=%+v successor=%+v", predecessor, successor)
	}
}

func requireCoordinatorHandoffNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
