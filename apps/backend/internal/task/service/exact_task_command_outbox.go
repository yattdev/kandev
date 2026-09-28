package service

import (
	"context"

	sqliterepo "github.com/kandev/kandev/internal/task/repository/sqlite"
)

type exactTaskCommandOutboxRepository interface {
	ClaimExactTaskCommandOutbox(context.Context, string) (*sqliterepo.ExactTaskCommandOutboxRecord, error)
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
	release := true
	defer func() {
		if release {
			_ = outbox.ReleaseExactTaskCommandOutboxClaim(ctx, auditID)
		}
	}()
	if !record.Delivered {
		task, taskErr := s.GetTask(ctx, record.TaskID)
		if taskErr != nil || task == nil || task.WorkspaceID != record.WorkspaceID || task.ResourceVersion != record.ResourceVersion {
			return sqliterepo.ErrExactTaskCommandUnavailable
		}
		if err = s.publishTaskEventNow(ctx, "task.updated", task, nil, nil, nil, nil); err != nil {
			return err
		}
		if err = outbox.RecordExactTaskCommandOutboxDelivery(ctx, auditID); err != nil {
			return err
		}
	}
	if err = outbox.AcknowledgeExactTaskCommandOutbox(ctx, auditID); err != nil {
		return err
	}
	release = false
	return nil
}
