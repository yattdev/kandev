package service

import (
	"context"

	"github.com/google/uuid"
	sqliterepo "github.com/kandev/kandev/internal/task/repository/sqlite"
)

var exactTaskCommandEventNamespace = uuid.MustParse("d4edb56a-514d-4d34-bd9e-0372ec654613")

func exactTaskCommandEventID(auditID string) string {
	return uuid.NewSHA1(exactTaskCommandEventNamespace, []byte(auditID)).String()
}

type exactTaskCommandOutboxRepository interface {
	ClaimExactTaskCommandOutbox(context.Context, string) (*sqliterepo.ExactTaskCommandOutboxRecord, error)
	BeginExactTaskCommandOutboxDelivery(context.Context, string) error
	ResetExactTaskCommandOutboxDelivery(context.Context, string) error
	RecordExactTaskCommandOutboxDelivery(context.Context, string) error
	AcknowledgeExactTaskCommandOutbox(context.Context, string) error
	ReleaseExactTaskCommandOutboxClaim(context.Context, string) error
}

// PublishExactTaskCommandUpdate is the task-service-owned completion path for
// an exact command's durable outbox row. It reads the committed row before
// publishing task.updated and acknowledges the outbox only after publication.
func (s *Service) PublishExactTaskCommandUpdate(ctx context.Context, auditID string) error {
	if s.eventBus == nil {
		return sqliterepo.ErrExactTaskCommandUnavailable
	}
	outbox, ok := s.tasks.(exactTaskCommandOutboxRepository)
	if !ok {
		return sqliterepo.ErrExactTaskCommandUnavailable
	}
	record, err := outbox.ClaimExactTaskCommandOutbox(ctx, auditID)
	if err != nil || record == nil {
		return err
	}
	if record.DeliveryStarted && !record.Delivered {
		return sqliterepo.ErrExactTaskCommandUnavailable
	}
	release := true
	defer func() {
		if release {
			_ = outbox.ReleaseExactTaskCommandOutboxClaim(ctx, auditID)
		}
	}()
	if err = s.deliverExactTaskCommandUpdate(ctx, outbox, record, auditID); err != nil {
		return err
	}
	if err = outbox.AcknowledgeExactTaskCommandOutbox(ctx, auditID); err != nil {
		return err
	}
	release = false
	return nil
}

func (s *Service) deliverExactTaskCommandUpdate(ctx context.Context, outbox exactTaskCommandOutboxRepository, record *sqliterepo.ExactTaskCommandOutboxRecord, auditID string) error {
	if record.Delivered {
		return nil
	}
	task, err := s.GetTask(ctx, record.TaskID)
	if err != nil || task == nil || task.WorkspaceID != record.WorkspaceID || task.ResourceVersion != record.ResourceVersion {
		return sqliterepo.ErrExactTaskCommandUnavailable
	}
	if err = outbox.BeginExactTaskCommandOutboxDelivery(ctx, auditID); err != nil {
		return err
	}
	if err = s.publishTaskEventNowWithID(ctx, "task.updated", task, nil, nil, nil, nil, exactTaskCommandEventID(auditID)); err != nil {
		_ = outbox.ResetExactTaskCommandOutboxDelivery(ctx, auditID)
		return err
	}
	return outbox.RecordExactTaskCommandOutboxDelivery(ctx, auditID)
}
