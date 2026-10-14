package orchestrator

import (
	"testing"

	"github.com/kandev/kandev/internal/orchestrator/executor"
	"github.com/kandev/kandev/internal/task/models"
	wfmodels "github.com/kandev/kandev/internal/workflow/models"
	"github.com/stretchr/testify/require"
)

// Contract coverage for the prompt producer and normal dispatch boundary.
func TestGatePromptCarriesReservedCompletionTurn(t *testing.T) {
	repo := setupTestRepo(t)
	getter := newMockStepGetter()
	getter.steps["gate"] = &wfmodels.WorkflowStep{ID: "gate", CompletionGatePolicy: wfmodels.CompletionGatePolicyReview}
	manager := &mockAgentManager{isAgentRunning: true}
	svc := createTestServiceWithAgent(repo, getter, newMockTaskRepo(), manager)
	svc.executor = executor.NewExecutor(manager, repo, testLogger(), executor.ExecutorConfig{})
	svc.turnService = &repoBackedTurnService{repo: repo}
	seedTaskAndSession(t, repo, "task1", "session1", models.TaskSessionStateWaitingForInput)
	task, err := repo.GetTask(t.Context(), "task1")
	require.NoError(t, err)
	task.WorkflowStepID = "gate"
	require.NoError(t, repo.UpdateTask(t.Context(), task))
	session, err := repo.GetTaskSession(t.Context(), "session1")
	require.NoError(t, err)
	session.AgentExecutionID = "exec-1"
	seedExecutorRunning(t, repo, session.ID, session.TaskID, "exec-1")
	require.NoError(t, repo.UpdateTaskSession(t.Context(), session))
	_, err = svc.PromptTask(t.Context(), task.ID, session.ID, "Review current source", "", false, nil, false)
	require.NoError(t, err)
	require.Len(t, manager.capturedPrompts, 1)
	require.Contains(t, manager.capturedPrompts[0], "Kandev completion turn ID: turn-session1")
	require.Contains(t, manager.capturedPrompts[0], "Review current source")
}
