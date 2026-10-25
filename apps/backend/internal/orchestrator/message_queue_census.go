package orchestrator

import (
	"context"
	"errors"

	"github.com/kandev/kandev/internal/agentctl/types/streams"
	"github.com/kandev/kandev/internal/orchestrator/messagequeue"
	"github.com/kandev/kandev/internal/task/models"
)

var ErrActiveQueueCensusDenied = errors.New("active queue census unavailable for this execution")

// GetActiveQueueCensus observes only the running stream's own queue. The
// context's execution identity is installed by the stream gateway, not by an
// MCP argument.
func (s *Service) GetActiveQueueCensus(ctx context.Context) (*messagequeue.ActiveQueueCensus, error) {
	execution, ok := streams.MCPExecutionContextFromContext(ctx)
	if !ok || execution.ExecutionID == "" || execution.TaskID == "" || execution.SessionID == "" || s == nil {
		return nil, ErrActiveQueueCensusDenied
	}
	proof, err := s.activeQueueCensusProof(ctx, execution)
	if err != nil {
		return nil, ErrActiveQueueCensusDenied
	}
	if s.messageQueue == nil {
		return nil, ErrActiveQueueCensusDenied
	}
	result, err := s.messageQueue.GetActiveQueueCensus(ctx, proof)
	if err != nil {
		return nil, err
	}
	current, err := s.activeQueueCensusProof(ctx, execution)
	if err != nil || current != proof {
		return nil, ErrActiveQueueCensusDenied
	}
	return result, nil
}

func (s *Service) activeQueueCensusProof(ctx context.Context, execution streams.MCPExecutionContext) (messagequeue.ActiveQueueCensusProof, error) {
	if s.repo == nil || s.authorizeTaskSessionPair(ctx, execution.TaskID, execution.SessionID) != nil {
		return messagequeue.ActiveQueueCensusProof{}, ErrActiveQueueCensusDenied
	}
	task, err := s.repo.GetTask(ctx, execution.TaskID)
	if err != nil || !validCensusTask(task) {
		return messagequeue.ActiveQueueCensusProof{}, ErrActiveQueueCensusDenied
	}
	session, err := s.repo.GetTaskSession(ctx, execution.SessionID)
	if err != nil || !validCensusSession(session, execution) {
		return messagequeue.ActiveQueueCensusProof{}, ErrActiveQueueCensusDenied
	}
	running, err := s.repo.GetExecutorRunningBySessionID(ctx, execution.SessionID)
	if err != nil || !validCensusRunning(running, execution) {
		return messagequeue.ActiveQueueCensusProof{}, ErrActiveQueueCensusDenied
	}
	return messagequeue.ActiveQueueCensusProof{Identity: messagequeue.QueueSessionIdentity{
		TaskID: execution.TaskID, SessionID: execution.SessionID, SessionIncarnationID: session.QueueIncarnationID,
	}, ExecutionID: execution.ExecutionID, WorkspaceID: task.WorkspaceID}, nil
}

func validCensusTask(task *models.Task) bool {
	return task != nil && task.ArchivedAt == nil && task.WorkspaceID != "" && !models.IsTerminalTaskState(task.State)
}

func validCensusSession(session *models.TaskSession, execution streams.MCPExecutionContext) bool {
	return session != nil && session.TaskID == execution.TaskID &&
		session.State == models.TaskSessionStateRunning && session.QueueIncarnationID != ""
}

func validCensusRunning(running *models.ExecutorRunning, execution streams.MCPExecutionContext) bool {
	return running != nil && running.TaskID == execution.TaskID &&
		running.AgentExecutionID == execution.ExecutionID && running.Status == models.ExecutorRunningStatusRunning
}
