package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/agentctl/types/streams"
	"github.com/kandev/kandev/internal/orchestrator/messagequeue"
	"github.com/kandev/kandev/internal/task/models"
	v1 "github.com/kandev/kandev/pkg/api/v1"
	"github.com/stretchr/testify/require"
)

// @covers AC-TASKS-ACTIVE-QUEUE-CENSUS-001.2
func TestActiveQueueCensusRejectsMissingExecutionBeforeDependencies(t *testing.T) {
	var service *Service
	reader, ok := interface{}(service).(interface {
		GetActiveQueueCensus(context.Context) (*messagequeue.ActiveQueueCensus, error)
	})
	require.True(t, ok, "orchestrator must expose an active-owner census guard")
	result, err := reader.GetActiveQueueCensus(context.Background())
	require.Nil(t, result)
	require.Error(t, err)
}

// @covers AC-TASKS-ACTIVE-QUEUE-CENSUS-001.1 AC-TASKS-ACTIVE-QUEUE-CENSUS-001.2
func TestActiveQueueCensusRequiresCurrentRunningExecution(t *testing.T) {
	ctx := context.Background()
	repo := setupTestRepo(t)
	seedSession(t, repo, "task", "session", "step", "incarnation")
	session, err := repo.GetTaskSession(ctx, "session")
	require.NoError(t, err)
	queueRepo := newAuthoritativeMemoryRepository(repo)
	queue := messagequeue.NewService(queueRepo, messagequeue.DefaultMaxPerSession, testLogger())
	identity := messagequeue.QueueSessionIdentity{TaskID: "task", SessionID: "session", SessionIncarnationID: session.QueueIncarnationID}
	bound, err := queueRepo.ResolveSessionIdentity(ctx, "task", "session")
	require.NoError(t, err)
	require.Equal(t, identity, bound)
	require.NoError(t, queueRepo.InsertForSession(ctx, identity, &messagequeue.QueuedMessage{
		ID: "one", TaskID: "task", SessionID: "session", Content: "private prompt",
	}, messagequeue.DefaultMaxPerSession))
	require.NoError(t, repo.UpsertExecutorRunning(ctx, &models.ExecutorRunning{
		ID: "running", TaskID: "task", SessionID: "session", AgentExecutionID: "current",
		Status: models.ExecutorRunningStatusRunning, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}))
	svc := NewService(DefaultServiceConfig(), nil, &mockAgentManager{}, newMockTaskRepo(), repo, nil, nil, queue, testLogger())
	current := streams.WithMCPExecutionContext(ctx, streams.MCPExecutionContext{
		TaskID: "task", SessionID: "session", ExecutionID: "current",
	})
	result, err := svc.GetActiveQueueCensus(current)
	require.NoError(t, err)
	require.Equal(t, 1, result.Count)
	require.Equal(t, "one", result.Entries[0].ID)

	stale := streams.WithMCPExecutionContext(ctx, streams.MCPExecutionContext{
		TaskID: "task", SessionID: "session", ExecutionID: "stale",
	})
	denied, err := svc.GetActiveQueueCensus(stale)
	require.ErrorIs(t, err, ErrActiveQueueCensusDenied)
	require.Nil(t, denied)

	task, err := repo.GetTask(ctx, "task")
	require.NoError(t, err)
	task.State = v1.TaskStateCompleted
	require.NoError(t, repo.UpdateTask(ctx, task))
	denied, err = svc.GetActiveQueueCensus(current)
	require.ErrorIs(t, err, ErrActiveQueueCensusDenied)
	require.Nil(t, denied)
}
