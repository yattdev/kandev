package service

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/events"
)

func TestPublishExactTaskCommandUpdatePublishesOnceAfterCommittedOutbox(t *testing.T) {
	ctx := context.Background()
	svc, eventBus, repo := createTestService(t)
	createTaskWithoutRepositories(t, ctx, repo)
	task, err := repo.GetTask(ctx, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.DB().ExecContext(ctx, `INSERT INTO exact_task_command_outbox(audit_id, task_id, workspace_id, resource_version) VALUES (?, ?, ?, ?)`, "audit-1", task.ID, task.WorkspaceID, task.ResourceVersion); err != nil {
		t.Fatal(err)
	}
	eventBus.ClearEvents()
	if err = svc.PublishExactTaskCommandUpdate(ctx, "audit-1"); err != nil {
		t.Fatalf("PublishExactTaskCommandUpdate: %v", err)
	}
	published := eventBus.GetPublishedEvents()
	if len(published) != 1 || published[0].Type != events.TaskUpdated {
		t.Fatalf("published events = %#v, want one task.updated", published)
	}
	if err = svc.PublishExactTaskCommandUpdate(ctx, "audit-1"); err != nil {
		t.Fatalf("exact command replay publication: %v", err)
	}
	if got := len(eventBus.GetPublishedEvents()); got != 1 {
		t.Fatalf("published events after replay = %d, want 1", got)
	}
}

func TestPublishExactTaskCommandUpdateHasNoEventWithoutCommittedOutbox(t *testing.T) {
	ctx := context.Background()
	svc, eventBus, repo := createTestService(t)
	createTaskWithoutRepositories(t, ctx, repo)
	eventBus.ClearEvents()
	if err := svc.PublishExactTaskCommandUpdate(ctx, "rolled-back-audit"); err == nil {
		t.Fatal("missing outbox row was published")
	}
	if got := len(eventBus.GetPublishedEvents()); got != 0 {
		t.Fatalf("published events = %d, want 0", got)
	}
}
