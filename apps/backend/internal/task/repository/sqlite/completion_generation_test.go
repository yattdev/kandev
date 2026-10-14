package sqlite

import (
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestCompletionSignalGenerationSQLite(t *testing.T) {
	runCompletionGeneration(t, newTurnStepStampTestRepo(t))
}
func TestCompletionSignalGenerationPostgres(t *testing.T) {
	db := testutil.OpenIsolatedPostgres(t, testutil.PostgresDSNFromEnv(t))
	repo, err := NewWithDB(db, db, nil)
	require.NoError(t, err)
	runCompletionGeneration(t, repo)
}
func runCompletionGeneration(t *testing.T, repo *Repository) {
	seedTurnStepStampSession(t, repo, "task-generation", "gate", "session-generation")
	makeTurn := func(id string) {
		_, err := repo.CreateTurnWithStepStamp(t.Context(), &models.Turn{ID: id, TaskID: "task-generation", TaskSessionID: "session-generation"})
		require.NoError(t, err)
	}
	claim := func(turn string) (bool, error) {
		return repo.SetSessionMetadataKeyIfAbsentOrDifferentStepIfTaskAtStep(t.Context(), "task-generation", "session-generation", models.SessionMetaKeyPendingStepCompletion, "gate", models.PendingStepCompletionSignal{StepID: "gate", TurnID: turn, Verdict: "PASS"})
	}
	makeTurn("first")
	stored, err := claim("first")
	require.NoError(t, err)
	require.True(t, stored)
	stored, err = claim("first")
	require.NoError(t, err)
	require.False(t, stored)
	task, err := repo.GetTask(t.Context(), "task-generation")
	require.NoError(t, err)
	task.WorkflowStepID = "other"
	require.NoError(t, repo.UpdateTask(t.Context(), task))
	task.WorkflowStepID = "gate"
	require.NoError(t, repo.UpdateTask(t.Context(), task))
	_, err = claim("first")
	require.Error(t, err, "leaving and returning to the same step invalidates its earlier generation")
	makeTurn("second")
	_, err = claim("first")
	require.Error(t, err)
	stored, err = claim("second")
	require.NoError(t, err)
	require.True(t, stored)
	session, err := repo.GetTaskSession(t.Context(), "session-generation")
	require.NoError(t, err)
	signal, has := models.LoadPendingStepSignal(session.Metadata)
	require.True(t, has)
	require.Equal(t, "second", signal.TurnID)
}
