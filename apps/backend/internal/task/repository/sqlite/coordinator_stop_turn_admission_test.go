package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestCoordinatorStopPendingBlankLaunchRejectsTurnReservation(t *testing.T) {
	assertCoordinatorStopTurnReservation(t, newRepoForSessionTests(t))
}

func TestPostgresCoordinatorStopPendingBlankLaunchRejectsTurnReservation(t *testing.T) {
	db := testutil.OpenIsolatedPostgres(t, testutil.PostgresDSNFromEnv(t))
	repo, err := NewWithDB(db, db, nil)
	require.NoError(t, err)
	assertCoordinatorStopTurnReservation(t, repo)
}

func assertCoordinatorStopTurnReservation(t *testing.T, repo *Repository) {
	t.Helper()
	ctx := context.Background()
	writers := map[string]func(*models.Turn) error{
		"plain":        func(turn *models.Turn) error { return repo.CreateTurn(ctx, turn) },
		"step":         func(turn *models.Turn) error { _, err := repo.CreateTurnWithStepStamp(ctx, turn); return err },
		"conversation": func(turn *models.Turn) error { _, err := repo.CreateTurnWithConversationReceipt(ctx, turn); return err },
		"step conversation": func(turn *models.Turn) error {
			_, _, err := repo.CreateTurnWithStepStampConversationReceipt(ctx, turn)
			return err
		},
	}
	for name, write := range writers {
		t.Run(name, func(t *testing.T) {
			task, parent, session := "turn-task-"+name, "turn-parent-"+name, "turn-session-"+name
			require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: task, Title: "launch"}))
			require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: parent, Title: "parent"}))
			require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{ID: session, TaskID: task, State: models.TaskSessionStateStarting}))
			request := models.CoordinatorStopRequest{TaskID: task, ParentTaskID: parent, OperationID: "turn-stop"}
			_, candidates, _, err := repo.CaptureCoordinatorStopRequestWithCandidates(ctx, request)
			require.NoError(t, err)
			require.Empty(t, candidates[0].TurnID)
			lifecycleTurn := &models.Turn{ID: "lifecycle-" + name, TaskID: task, TaskSessionID: session, StartedAt: time.Now().UTC(), Metadata: map[string]interface{}{models.TurnMetaKeyLifecycleOnly: true}}
			require.NoError(t, write(lifecycleTurn), "lifecycle bookkeeping does not reserve a launch turn")
			before, err := repo.GetTaskSession(ctx, session)
			require.NoError(t, err)
			turn := &models.Turn{ID: "rejected-" + name, TaskID: task, TaskSessionID: session, StartedAt: time.Now().UTC()}
			require.ErrorIs(t, write(turn), models.ErrExecutionStopFenced)
			_, err = repo.GetTurn(ctx, turn.ID)
			require.Error(t, err)
			after, err := repo.GetTaskSession(ctx, session)
			require.NoError(t, err)
			require.Equal(t, before, after)
			_, saved, _, err := repo.CaptureCoordinatorStopRequestWithCandidates(ctx, request)
			require.NoError(t, err)
			require.Equal(t, candidates, saved)
			changed, err := repo.FenceCoordinatorStopSessionForRequest(ctx, task, session, request.OperationID)
			require.NoError(t, err)
			require.True(t, changed)
			preserved, err := repo.GetTurn(ctx, lifecycleTurn.ID)
			require.NoError(t, err)
			require.Nil(t, preserved.CompletedAt)
			require.NoError(t, repo.CompleteCoordinatorStopRequest(ctx, task, request.OperationID, parent, "incomplete"))
			require.NoError(t, repo.UpdateTaskSessionState(ctx, session, models.TaskSessionStateStarting, ""))
			turn.ID = "authorized-" + name
			require.NoError(t, write(turn), "completed historical stop must preserve an authorized successor")
		})
	}
}
