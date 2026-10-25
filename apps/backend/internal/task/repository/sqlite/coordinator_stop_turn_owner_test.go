package sqlite

import (
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestCoordinatorStopTurnAdmissionUsesSessionOwner(t *testing.T) {
	assertCoordinatorStopTurnOwner(t, newRepoForSessionTests(t))
}

func TestPostgresCoordinatorStopTurnAdmissionUsesSessionOwner(t *testing.T) {
	db := testutil.OpenIsolatedPostgres(t, testutil.PostgresDSNFromEnv(t))
	repo, err := NewWithDB(db, db, nil)
	require.NoError(t, err)
	assertCoordinatorStopTurnOwner(t, repo)
}

func assertCoordinatorStopTurnOwner(t *testing.T, repo *Repository) {
	t.Helper()
	ctx := t.Context()
	writers := map[string]func(*models.Turn) (bool, error){
		"plain": func(turn *models.Turn) (bool, error) { return false, repo.CreateTurn(ctx, turn) },
		"step":  func(turn *models.Turn) (bool, error) { return repo.CreateTurnWithStepStamp(ctx, turn) },
		"conversation": func(turn *models.Turn) (bool, error) {
			_, err := repo.CreateTurnWithConversationReceipt(ctx, turn)
			return false, err
		},
		"step conversation": func(turn *models.Turn) (bool, error) {
			stamped, _, err := repo.CreateTurnWithStepStampConversationReceipt(ctx, turn)
			return stamped, err
		},
	}
	for name, write := range writers {
		t.Run(name, func(t *testing.T) {
			task, parent, session := "owner-task-"+name, "owner-parent-"+name, "owner-session-"+name
			require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: task, Title: "owner", WorkflowStepID: "owner-step"}))
			require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: parent, Title: "parent"}))
			require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{ID: session, TaskID: task, State: models.TaskSessionStateStarting}))
			turn := &models.Turn{ID: "degraded-" + name, TaskID: "absent-telemetry-task", TaskSessionID: session}
			stamped, err := write(turn)
			require.NoError(t, err, "missing telemetry task preserves turn creation")
			require.False(t, stamped)
			stored, err := repo.GetTurn(ctx, turn.ID)
			require.NoError(t, err)
			require.Equal(t, turn.TaskID, stored.TaskID)
			require.NotContains(t, stored.Metadata, models.TurnMetaKeyWorkflowStepIDAtStart)
			require.NoError(t, repo.CompleteTurn(ctx, turn.ID))
			request := models.CoordinatorStopRequest{TaskID: task, ParentTaskID: parent, OperationID: "owner-stop"}
			_, candidates, _, err := repo.CaptureCoordinatorStopRequestWithCandidates(ctx, request)
			require.NoError(t, err)
			require.Empty(t, candidates[0].TurnID)
			before, err := repo.GetTaskSession(ctx, session)
			require.NoError(t, err)
			turn.ID = "refused-" + name
			turn.CompletedAt = nil
			_, err = write(turn)
			require.ErrorIs(t, err, models.ErrExecutionStopFenced, "supplied telemetry identity cannot bypass the session owner's stop")
			_, err = repo.GetTurn(ctx, turn.ID)
			require.Error(t, err)
			after, err := repo.GetTaskSession(ctx, session)
			require.NoError(t, err)
			require.Equal(t, before, after)
			_, saved, _, err := repo.CaptureCoordinatorStopRequestWithCandidates(ctx, request)
			require.NoError(t, err)
			require.Equal(t, candidates, saved)
		})
	}
}
