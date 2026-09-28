package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/events"
	"github.com/kandev/kandev/internal/task/repository"
	sqliterepo "github.com/kandev/kandev/internal/task/repository/sqlite"
)

type acknowledgeFailingExactTaskCommandOutbox struct {
	repository.TaskRepository
	outbox       *sqliterepo.Repository
	failAck      bool
	failDelivery bool
}

func (r *acknowledgeFailingExactTaskCommandOutbox) ClaimExactTaskCommandOutbox(ctx context.Context, auditID string) (*sqliterepo.ExactTaskCommandOutboxRecord, error) {
	return r.outbox.ClaimExactTaskCommandOutbox(ctx, auditID)
}

func (r *acknowledgeFailingExactTaskCommandOutbox) RecordExactTaskCommandOutboxDelivery(ctx context.Context, auditID string) error {
	if r.failDelivery {
		r.failDelivery = false
		return errors.New("delivery record failed")
	}
	return r.outbox.RecordExactTaskCommandOutboxDelivery(ctx, auditID)
}

func (r *acknowledgeFailingExactTaskCommandOutbox) BeginExactTaskCommandOutboxDelivery(ctx context.Context, auditID string) error {
	return r.outbox.BeginExactTaskCommandOutboxDelivery(ctx, auditID)
}

func (r *acknowledgeFailingExactTaskCommandOutbox) ResetExactTaskCommandOutboxDelivery(ctx context.Context, auditID string) error {
	return r.outbox.ResetExactTaskCommandOutboxDelivery(ctx, auditID)
}

func (r *acknowledgeFailingExactTaskCommandOutbox) AcknowledgeExactTaskCommandOutbox(ctx context.Context, auditID string) error {
	if r.failAck {
		return errors.New("acknowledgement failed")
	}
	return r.outbox.AcknowledgeExactTaskCommandOutbox(ctx, auditID)
}

func (r *acknowledgeFailingExactTaskCommandOutbox) ReleaseExactTaskCommandOutboxClaim(ctx context.Context, auditID string) error {
	return r.outbox.ReleaseExactTaskCommandOutboxClaim(ctx, auditID)
}

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

func TestPublishExactTaskCommandUpdateRetriesAfterEventBusFailure(t *testing.T) {
	ctx := context.Background()
	svc, eventBus, repo := createTestService(t)
	createTaskWithoutRepositories(t, ctx, repo)
	task, err := repo.GetTask(ctx, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.DB().ExecContext(ctx, `INSERT INTO exact_task_command_outbox(audit_id, task_id, workspace_id, resource_version) VALUES (?, ?, ?, ?)`, "audit-retry", task.ID, task.WorkspaceID, task.ResourceVersion); err != nil {
		t.Fatal(err)
	}
	svc.eventBus = &taskPublicationBarrierBus{MockEventBus: eventBus, failNext: true}
	if err = svc.PublishExactTaskCommandUpdate(ctx, "audit-retry"); err == nil {
		t.Fatal("event bus failure acknowledged the outbox row")
	}
	var publishedAt any
	if err = repo.DB().QueryRowContext(ctx, `SELECT published_at FROM exact_task_command_outbox WHERE audit_id = ?`, "audit-retry").Scan(&publishedAt); err != nil {
		t.Fatal(err)
	}
	if publishedAt != nil {
		t.Fatal("failed publication acknowledged the outbox row")
	}
	svc.eventBus = eventBus
	if err = svc.PublishExactTaskCommandUpdate(ctx, "audit-retry"); err != nil {
		t.Fatalf("retry publication: %v", err)
	}
	if got := len(eventBus.GetPublishedEvents()); got != 1 {
		t.Fatalf("published events after retry = %d, want 1", got)
	}
	if err = svc.PublishExactTaskCommandUpdate(ctx, "audit-retry"); err != nil {
		t.Fatalf("replay publication: %v", err)
	}
	if got := len(eventBus.GetPublishedEvents()); got != 1 {
		t.Fatalf("published events after replay = %d, want 1", got)
	}
}

func TestPublishExactTaskCommandUpdateRecoversAcknowledgementFailureWithoutRepublishing(t *testing.T) {
	ctx := context.Background()
	svc, eventBus, repo := createTestService(t)
	createTaskWithoutRepositories(t, ctx, repo)
	task, err := repo.GetTask(ctx, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.DB().ExecContext(ctx, `INSERT INTO exact_task_command_outbox(audit_id, task_id, workspace_id, resource_version) VALUES (?, ?, ?, ?)`, "audit-ack-retry", task.ID, task.WorkspaceID, task.ResourceVersion); err != nil {
		t.Fatal(err)
	}
	failing := &acknowledgeFailingExactTaskCommandOutbox{TaskRepository: repo, outbox: repo, failAck: true}
	svc.tasks = failing
	if err = svc.PublishExactTaskCommandUpdate(ctx, "audit-ack-retry"); err == nil {
		t.Fatal("acknowledgement failure completed the outbox row")
	}
	if got := len(eventBus.GetPublishedEvents()); got != 1 {
		t.Fatalf("published events after acknowledgement failure = %d, want 1", got)
	}
	var deliveredAt, publishedAt any
	if err = repo.DB().QueryRowContext(ctx, `SELECT delivered_at, published_at FROM exact_task_command_outbox WHERE audit_id = ?`, "audit-ack-retry").Scan(&deliveredAt, &publishedAt); err != nil {
		t.Fatal(err)
	}
	if deliveredAt == nil || publishedAt != nil {
		t.Fatalf("outbox state after acknowledgement failure = delivered:%v published:%v, want delivered and unacknowledged", deliveredAt, publishedAt)
	}

	// A reconstructed service must finish the durable delivery without
	// republishing the already-recorded event.
	restarted := &Service{tasks: repo, eventBus: eventBus}
	if err = restarted.PublishExactTaskCommandUpdate(ctx, "audit-ack-retry"); err != nil {
		t.Fatalf("restart acknowledgement retry: %v", err)
	}
	if got := len(eventBus.GetPublishedEvents()); got != 1 {
		t.Fatalf("published events after restart acknowledgement retry = %d, want 1", got)
	}
	if err = repo.DB().QueryRowContext(ctx, `SELECT published_at FROM exact_task_command_outbox WHERE audit_id = ?`, "audit-ack-retry").Scan(&publishedAt); err != nil {
		t.Fatal(err)
	}
	if publishedAt == nil {
		t.Fatal("restart acknowledgement retry left the delivered outbox row unacknowledged")
	}
	if err = restarted.PublishExactTaskCommandUpdate(ctx, "audit-ack-retry"); err != nil {
		t.Fatalf("replay after acknowledgement retry: %v", err)
	}
	if got := len(eventBus.GetPublishedEvents()); got != 1 {
		t.Fatalf("published events after replay = %d, want 1", got)
	}
}

func TestPublishExactTaskCommandUpdateDoesNotRepublishAfterAmbiguousDelivery(t *testing.T) {
	ctx := context.Background()
	svc, eventBus, repo := createTestService(t)
	createTaskWithoutRepositories(t, ctx, repo)
	task, err := repo.GetTask(ctx, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.DB().ExecContext(ctx, `INSERT INTO exact_task_command_outbox(audit_id, task_id, workspace_id, resource_version) VALUES (?, ?, ?, ?)`, "audit-delivery-record-retry", task.ID, task.WorkspaceID, task.ResourceVersion); err != nil {
		t.Fatal(err)
	}
	failing := &acknowledgeFailingExactTaskCommandOutbox{TaskRepository: repo, outbox: repo, failDelivery: true}
	svc.tasks = failing
	if err = svc.PublishExactTaskCommandUpdate(ctx, "audit-delivery-record-retry"); err == nil {
		t.Fatal("delivery record failure completed the outbox row")
	}
	if _, err = repo.DB().ExecContext(ctx, `UPDATE exact_task_command_outbox SET claimed_at = ? WHERE audit_id = ?`, time.Now().UTC().Add(-2*time.Minute), "audit-delivery-record-retry"); err != nil {
		t.Fatal(err)
	}
	if err = svc.PublishExactTaskCommandUpdate(ctx, "audit-delivery-record-retry"); !errors.Is(err, sqliterepo.ErrExactTaskCommandUnavailable) {
		t.Fatalf("retry after ambiguous delivery = %v, want pending/unavailable", err)
	}
	published := eventBus.GetPublishedEvents()
	if len(published) != 1 {
		t.Fatalf("published task.updated events = %d, want one", len(published))
	}
	if published[0].ID == "" || published[0].ID != exactTaskCommandEventID("audit-delivery-record-retry") {
		t.Fatalf("event ID = %q, want stable identity %q", published[0].ID, exactTaskCommandEventID("audit-delivery-record-retry"))
	}
	if published[0].Type != events.TaskUpdated {
		t.Fatalf("published event type = %q, want task.updated", published[0].Type)
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
