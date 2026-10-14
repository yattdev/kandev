package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/testutil"
)

func TestCompletionAdmissionSQLite(t *testing.T) {
	testCompletionAdmissionCases(t, newTurnStepStampTestRepo)
}
func TestCompletionAdmissionPostgres(t *testing.T) {
	dsn := testutil.PostgresDSNFromEnv(t)
	testCompletionAdmissionCases(t, func(t *testing.T) *Repository {
		db := testutil.OpenIsolatedPostgres(t, dsn)
		repo, err := NewWithDB(db, db, nil)
		require.NoError(t, err)
		return repo
	})
}
func testCompletionAdmissionCases(t *testing.T, build func(*testing.T) *Repository) {
	for _, cas := range []bool{false, true} {
		for _, scenario := range []string{"valid", "revisited", "new-turn", "blocked", "cleared"} {
			name := scenario + map[bool]string{false: "/legacy", true: "/cas"}[cas]
			t.Run(name, func(t *testing.T) {
				repo := build(t)
				ctx := seedCompletionAdmission(t, repo)
				alterCompletionAfterAdmission(t, repo, scenario)
				err := applyCompletionAdmission(repo, ctx, cas)
				if scenario == "valid" {
					require.NoError(t, err)
				} else {
					require.ErrorIs(t, err, models.ErrStaleCompletionTurn)
				}
				saved, err := repo.GetTask(t.Context(), "commit-task")
				require.NoError(t, err)
				want := "gate"
				if scenario == "valid" {
					want = "next"
				}
				require.Equal(t, want, saved.WorkflowStepID)
			})
		}
	}
}

func seedCompletionAdmission(t *testing.T, repo *Repository) context.Context {
	t.Helper()
	seedTurnStepStampSession(t, repo, "commit-task", "gate", "commit-session")
	_, err := repo.CreateTurnWithStepStamp(t.Context(), &models.Turn{ID: "commit-turn", TaskID: "commit-task", TaskSessionID: "commit-session"})
	require.NoError(t, err)
	signal := models.PendingStepCompletionSignal{StepID: "gate", TurnID: "commit-turn", Verdict: "PASS"}
	stored, err := repo.SetSessionMetadataKeyIfAbsentOrDifferentStepIfTaskAtStep(t.Context(), "commit-task", "commit-session", models.SessionMetaKeyPendingStepCompletion, "gate", signal)
	require.NoError(t, err)
	require.True(t, stored)
	valid, err := repo.CompletionTurnCurrent(t.Context(), "commit-task", "commit-session", "gate", "commit-turn")
	require.NoError(t, err)
	require.True(t, valid)
	return models.WithStepCompletionAdmission(t.Context(), models.StepCompletionAdmission{TaskID: "commit-task", SessionID: "commit-session", StepID: "gate", TurnID: "commit-turn"})
}

func alterCompletionAfterAdmission(t *testing.T, repo *Repository, scenario string) {
	t.Helper()
	switch scenario {
	case "revisited":
		task, err := repo.GetTask(t.Context(), "commit-task")
		require.NoError(t, err)
		task.WorkflowStepID = "other"
		require.NoError(t, repo.UpdateTask(t.Context(), task))
		task.WorkflowStepID = "gate"
		require.NoError(t, repo.UpdateTask(t.Context(), task))
	case "new-turn":
		_, err := repo.CreateTurnWithStepStamp(t.Context(), &models.Turn{ID: "new-turn", TaskID: "commit-task", TaskSessionID: "commit-session"})
		require.NoError(t, err)
	case "blocked":
		require.NoError(t, repo.SetSessionMetadataKey(t.Context(), "commit-session", models.SessionMetaKeyPendingStepCompletion, models.PendingStepCompletionSignal{StepID: "gate", TurnID: "commit-turn", Verdict: "BLOCKED"}))
	case "cleared":
		require.NoError(t, repo.SetSessionMetadataKey(t.Context(), "commit-session", models.SessionMetaKeyPendingStepCompletion, nil))
	}
}

func applyCompletionAdmission(repo *Repository, ctx context.Context, cas bool) error {
	task, err := repo.GetTask(ctx, "commit-task")
	if err != nil {
		return err
	}
	task.WorkflowStepID = "next"
	if cas {
		_, err = repo.UpdateTaskWithWorkflowStepAdmissionIfAtStep(ctx, task, "gate", "next", 0)
	} else {
		_, err = repo.UpdateTaskWithWorkflowStepAdmission(ctx, task, "gate", "next", 0)
	}
	return err
}
