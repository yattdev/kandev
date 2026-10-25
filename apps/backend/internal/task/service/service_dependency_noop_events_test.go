package service

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
)

func TestDependencyNoOpDoesNotPublishTaskUpdates(t *testing.T) {
	ctx := context.Background()
	svc, eventBus, repo := createTestService(t)
	setupTestTask(t, repo)
	if err := repo.CreateTask(ctx, &models.Task{
		ID: "task-peer", WorkspaceID: "ws-1", WorkflowID: "wf-123",
		WorkflowStepID: "step-123", Title: "Peer", Priority: "medium",
	}); err != nil {
		t.Fatal(err)
	}
	blockers := &mockBlockerRepo{}
	svc.SetBlockerRepository(blockers)

	if err := svc.AddDependency(ctx, "task-123", "task-peer"); err != nil {
		t.Fatal(err)
	}
	if len(eventBus.GetPublishedEvents()) != 2 {
		t.Fatalf("first add published %d events, want 2", len(eventBus.GetPublishedEvents()))
	}
	eventBus.ClearEvents()
	if err := svc.AddDependency(ctx, "task-123", "task-peer"); err != nil {
		t.Fatal(err)
	}
	if got := len(eventBus.GetPublishedEvents()); got != 0 {
		t.Fatalf("duplicate add published %d events, want 0", got)
	}
	if got := len(blockers.blockers); got != 1 {
		t.Fatalf("duplicate add left %d edges, want 1", got)
	}

	if err := svc.RemoveDependency(ctx, "task-123", "task-peer"); err != nil {
		t.Fatal(err)
	}
	if len(eventBus.GetPublishedEvents()) != 2 {
		t.Fatalf("first remove published %d events, want 2", len(eventBus.GetPublishedEvents()))
	}
	eventBus.ClearEvents()
	if err := svc.RemoveDependency(ctx, "task-123", "task-peer"); err != nil {
		t.Fatal(err)
	}
	if got := len(eventBus.GetPublishedEvents()); got != 0 {
		t.Fatalf("absent remove published %d events, want 0", got)
	}
}
