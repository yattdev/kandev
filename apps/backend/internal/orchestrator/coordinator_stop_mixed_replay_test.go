package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/orchestrator/messagequeue"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

// @covers AC-STOP-FENCE-005, AC-STOP-FENCE-007, AC-STOP-FENCE-008
func TestCoordinatorStopMixedLaunchFenceRetriesUnboundExactCandidate(t *testing.T) {
	for _, startup := range []bool{false, true} {
		t.Run(map[bool]string{false: "caller retry", true: "startup replay"}[startup], func(t *testing.T) {
			ctx := context.Background()
			repo, svc, stops := newLegacyStopFixture(t)
			now := time.Now().UTC()
			require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{ID: "a-launch", TaskID: "legacy-task", State: models.TaskSessionStateStarting, StartedAt: now, UpdatedAt: now}))
			request := models.CoordinatorStopRequest{TaskID: "legacy-task", ParentTaskID: "legacy-parent", OperationID: "mixed-replay"}
			_, candidates, _, err := repo.CaptureCoordinatorStopRequestWithCandidates(ctx, request)
			require.NoError(t, err)
			running, err := repo.GetExecutorRunningBySessionID(ctx, "legacy-session")
			require.NoError(t, err)
			require.NoError(t, repo.DeleteExecutorRunningBySessionID(ctx, "legacy-session"))
			first, err := svc.StopTaskForCoordinatorOperation(ctx, request.TaskID, request.ParentTaskID, request.OperationID)
			require.NoError(t, err)
			require.Equal(t, CoordinatorTaskStopStatusIncomplete, first.Status)
			require.Empty(t, first.Receipts)
			require.Len(t, first.SessionFences, 1)
			require.NoError(t, repo.UpdateTaskSessionState(ctx, "a-launch", models.TaskSessionStateStarting, ""))
			queued, err := svc.messageQueue.QueueMessage(ctx, "a-launch", request.TaskID, "successor message", "", messagequeue.QueuedByAgent, false, nil)
			require.NoError(t, err)
			beforeA := mustGetSession(t, repo, "a-launch")
			require.NoError(t, repo.UpsertExecutorRunning(ctx, running))
			if startup {
				require.NoError(t, svc.reconcilePendingCoordinatorStopRequestsOnStartup(ctx))
			} else {
				_, err = svc.StopTaskForCoordinatorOperation(ctx, request.TaskID, request.ParentTaskID, request.OperationID)
				require.NoError(t, err)
			}
			lookup, err := svc.GetCoordinatorStopReceipt(ctx, request.TaskID, request.ParentTaskID, request.OperationID)
			require.NoError(t, err)
			require.Equal(t, CoordinatorTaskStopStatusIncomplete, lookup.Status, "launch-only historical proof cannot certify terminal runtime state")
			require.Len(t, lookup.Receipts, 1, "a bound launch fence must not hide the unresolved exact candidate")
			require.Equal(t, models.CoordinatorStopOperationStatusStopped, lookup.Receipts[0].Status)
			require.Equal(t, "legacy-execution", lookup.Receipts[0].ExecutionID)
			require.Equal(t, first.SessionFences, lookup.SessionFences)
			require.Equal(t, beforeA, mustGetSession(t, repo, "a-launch"))
			afterQueue, err := svc.messageQueue.GetEntry(ctx, "a-launch", queued.ID)
			require.NoError(t, err)
			require.Equal(t, queued, afterQueue)
			require.EqualValues(t, 1, stops.Load())
			_, retryCandidates, _, err := repo.CaptureCoordinatorStopRequestWithCandidates(ctx, request)
			require.NoError(t, err)
			require.Equal(t, candidates, retryCandidates)
			_, err = svc.StopTaskForCoordinatorOperation(ctx, request.TaskID, request.ParentTaskID, request.OperationID)
			require.NoError(t, err)
			require.EqualValues(t, 1, stops.Load())
			require.Equal(t, beforeA, mustGetSession(t, repo, "a-launch"))
		})
	}
}
