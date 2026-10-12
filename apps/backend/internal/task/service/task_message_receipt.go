package service

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"time"

	"github.com/kandev/kandev/internal/events"
	"github.com/kandev/kandev/internal/orchestrator/messagequeue"
	"github.com/kandev/kandev/internal/task/models"
)

type taskMessageQueueWriter interface {
	CreateTaskMessageAndQueue(context.Context, *models.Message, *messagequeue.QueuedMessage, *models.Turn, models.TaskSessionState, int) error
}

// CreateTaskMessageQueuedIdempotent uses the existing message admission lease
// and immutable fingerprint to preserve an exact inter-task replay identity.
func (s *Service) CreateTaskMessageQueuedIdempotent(ctx context.Context, id string, req *CreateMessageRequest, queued *messagequeue.QueuedMessage, max int) (*models.Message, error) {
	if id == "" || req == nil || req.AuthorType != string(models.MessageAuthorUser) || queued == nil {
		return nil, errors.New("exact task message required")
	}
	if err := s.authorizeMessageCreate(ctx, req); err != nil {
		return nil, err
	}
	ctx, release, err := s.AcquireMessageAdmission(ctx, id)
	if err != nil {
		return nil, err
	}
	defer release()
	existing, found, err := s.findPlanCommentMessageReplay(ctx, id, req)
	if err != nil || found {
		return existing, err
	}
	session, err := s.getSessionWithRetry(ctx, req.TaskSessionID, id, messageCreateMaxRetries, messageCreateRetryDelay)
	if err != nil {
		return nil, err
	}
	message := &models.Message{ID: id, TaskID: session.TaskID, TaskSessionID: session.ID, AuthorType: models.MessageAuthorUser, AuthorID: s.resolveAuthorID(ctx, models.MessageAuthorUser, req.AuthorID), Content: req.Content, Type: models.MessageTypeMessage, Metadata: req.Metadata}
	turn := &models.Turn{ID: uuid.NewString(), TaskID: session.TaskID, TaskSessionID: session.ID, ExecutionProfileID: session.ExecutionProfileID, RouteGeneration: session.RouteGeneration, StartedAt: time.Now().UTC(), Metadata: turnStartRuntimeMetadata(session)}
	queued.ID = id
	queued.TaskID = message.TaskID
	queued.SessionID = message.TaskSessionID
	queued.Content = message.Content
	queued.QueuedBy = messagequeue.QueuedByUser
	writer, ok := s.messages.(taskMessageQueueWriter)
	if !ok {
		return nil, errors.New("atomic task message admission unavailable")
	}
	if err := writer.CreateTaskMessageAndQueue(ctx, message, queued, turn, req.ExpectedSessionState, max); err != nil {
		return nil, err
	}
	if message.TurnID == turn.ID {
		_ = s.PublishTurnStarted(ctx, turn)
	}
	_ = s.publishMessageEvent(ctx, events.MessageAdded, message)
	return message, nil
}
