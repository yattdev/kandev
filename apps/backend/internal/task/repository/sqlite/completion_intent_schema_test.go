package sqlite

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	dbutil "github.com/kandev/kandev/internal/db"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

func TestCompletionIntentSchemaCreatesAuditedRecoveryRecords(t *testing.T) {
	repo := newTurnStepStampTestRepo(t)

	for _, table := range []string{"session_completion_intents", "session_control_events"} {
		var name string
		err := repo.DB().QueryRow(`
			SELECT name
			FROM sqlite_master
			WHERE type = 'table' AND name = ?
		`, table).Scan(&name)
		require.NoError(t, err, "missing durable recovery table %q", table)
		require.Equal(t, table, name)
	}

	var identityIndex string
	err := repo.DB().QueryRow(`
		SELECT name
		FROM sqlite_master
		WHERE type = 'index'
		  AND tbl_name = 'session_completion_intents'
		  AND sql LIKE '%session_id, turn_id, workflow_step_transition_id%'
	`).Scan(&identityIndex)
	require.NoError(t, err, "completion intent must be unique per session, turn, and workflow entry")
}

func TestCompletionIntentSettlementTwoSQLiteHandles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settlement.db")
	open := func() *Repository {
		conn, err := dbutil.OpenSQLite(path)
		require.NoError(t, err)
		db := sqlx.NewDb(conn, "sqlite3")
		t.Cleanup(func() { _ = db.Close() })
		repo, err := NewWithDB(db, db, nil)
		require.NoError(t, err)
		return repo
	}
	first := open()
	second := open()
	seedTurnStepStampSession(t, first, "task", "step-a", "session")
	turn := &models.Turn{TaskSessionID: "session", TaskID: "task"}
	_, err := first.CreateTurnWithStepStamp(t.Context(), turn)
	require.NoError(t, err)
	_, _, entry, err := first.GetTaskWorkflowStepEntry(t.Context(), "task")
	require.NoError(t, err)
	_, claimed, err := first.ClaimStepCompletionSignalForTurn(t.Context(), "task", "session", turn.ID, models.PendingStepCompletionSignal{StepID: "step-a", WorkflowStepTransitionID: &entry, Summary: "done", SignaledAt: time.Now().UTC()})
	require.NoError(t, err)
	require.True(t, claimed)
	var intentID string
	require.NoError(t, first.DB().QueryRow(`SELECT id FROM session_completion_intents`).Scan(&intentID))
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, repo := range []*Repository{first, second} {
		wg.Go(func() {
			<-start
			errs <- repo.SettleCompletionIntent(t.Context(), intentID, models.SessionControlEvent{Authority: "direct_parent", EvidenceRevision: "test", EvidenceDigest: "digest", Outcome: "settled", Reason: "test"})
		})
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	var events int
	require.NoError(t, first.DB().QueryRow(`SELECT COUNT(*) FROM session_control_events WHERE completion_intent_id=?`, intentID).Scan(&events))
	require.Equal(t, 1, events)
}

func TestCompletionIntentCaptureUsesExactStampedTurn(t *testing.T) {
	repo := newTurnStepStampTestRepo(t)
	seedTurnStepStampSession(t, repo, "task", "step-a", "session")
	turn := &models.Turn{TaskSessionID: "session", TaskID: "task"}
	stamped, err := repo.CreateTurnWithStepStamp(t.Context(), turn)
	require.NoError(t, err)
	require.True(t, stamped)
	_, _, entry, err := repo.GetTaskWorkflowStepEntry(t.Context(), "task")
	require.NoError(t, err)

	signal := models.PendingStepCompletionSignal{
		StepID:                   "step-a",
		WorkflowStepTransitionID: &entry,
		Summary:                  "done",
		SignaledAt:               time.Now().UTC(),
	}
	_, claimed, err := repo.ClaimStepCompletionSignalForTurn(t.Context(), "task", "session", turn.ID, signal)
	require.NoError(t, err)
	require.True(t, claimed)

	var storedTurn string
	require.NoError(t, repo.DB().QueryRow(`
		SELECT turn_id FROM session_completion_intents
		WHERE session_id = ? AND workflow_step_transition_id = ?
	`, "session", entry).Scan(&storedTurn))
	require.Equal(t, turn.ID, storedTurn)

	_, claimed, err = repo.ClaimStepCompletionSignalForTurn(t.Context(), "task", "session", turn.ID, signal)
	require.NoError(t, err)
	require.False(t, claimed)
	var count int
	require.NoError(t, repo.DB().QueryRow(`SELECT COUNT(*) FROM session_completion_intents`).Scan(&count))
	require.Equal(t, 1, count)
}

func TestCompletionIntentSettlementAtomicallyClosesCapturedTurn(t *testing.T) {
	repo := newTurnStepStampTestRepo(t)
	seedTurnStepStampSession(t, repo, "task", "step-a", "session")
	turn := &models.Turn{TaskSessionID: "session", TaskID: "task"}
	stamped, err := repo.CreateTurnWithStepStamp(t.Context(), turn)
	require.NoError(t, err)
	require.True(t, stamped)
	_, _, entry, err := repo.GetTaskWorkflowStepEntry(t.Context(), "task")
	require.NoError(t, err)
	_, claimed, err := repo.ClaimStepCompletionSignalForTurn(t.Context(), "task", "session", turn.ID, models.PendingStepCompletionSignal{StepID: "step-a", WorkflowStepTransitionID: &entry, Summary: "done", SignaledAt: time.Now().UTC()})
	require.NoError(t, err)
	require.True(t, claimed)

	var intentID string
	require.NoError(t, repo.DB().QueryRow(`SELECT id FROM session_completion_intents`).Scan(&intentID))
	err = repo.SettleCompletionIntent(t.Context(), intentID, models.SessionControlEvent{
		ActorTaskID: "parent", ActorSessionID: "parent-session", Authority: "direct_parent",
		EvidenceRevision: "test", EvidenceDigest: "digest", Outcome: "settled", Reason: "test",
	})
	require.NoError(t, err)

	storedTurn, err := repo.GetTurn(t.Context(), turn.ID)
	require.NoError(t, err)
	require.NotNil(t, storedTurn.CompletedAt)
	session, err := repo.GetTaskSession(t.Context(), "session")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateWaitingForInput, session.State)
	var state string
	var events int
	require.NoError(t, repo.DB().QueryRow(`SELECT state FROM session_completion_intents WHERE id = ?`, intentID).Scan(&state))
	require.NoError(t, repo.DB().QueryRow(`SELECT COUNT(*) FROM session_control_events WHERE completion_intent_id = ?`, intentID).Scan(&events))
	require.Equal(t, models.CompletionIntentStateSettled, state)
	require.Equal(t, 1, events)
	require.NoError(t, repo.SettleCompletionIntent(t.Context(), intentID, models.SessionControlEvent{}))
	require.NoError(t, repo.DB().QueryRow(`SELECT COUNT(*) FROM session_control_events WHERE completion_intent_id = ?`, intentID).Scan(&events))
	require.Equal(t, 1, events)
}

func TestCompletionIntentSettlementRejectsStaleTurnWithoutClosingSuccessor(t *testing.T) {
	repo := newTurnStepStampTestRepo(t)
	seedTurnStepStampSession(t, repo, "task", "step-a", "session")
	first := &models.Turn{TaskSessionID: "session", TaskID: "task"}
	_, err := repo.CreateTurnWithStepStamp(t.Context(), first)
	require.NoError(t, err)
	_, _, entry, err := repo.GetTaskWorkflowStepEntry(t.Context(), "task")
	require.NoError(t, err)
	_, claimed, err := repo.ClaimStepCompletionSignalForTurn(t.Context(), "task", "session", first.ID, models.PendingStepCompletionSignal{StepID: "step-a", WorkflowStepTransitionID: &entry, Summary: "done", SignaledAt: time.Now().UTC()})
	require.NoError(t, err)
	require.True(t, claimed)
	var intentID string
	require.NoError(t, repo.DB().QueryRow(`SELECT id FROM session_completion_intents`).Scan(&intentID))
	require.NoError(t, repo.CompleteTurn(t.Context(), first.ID))
	successor := &models.Turn{TaskSessionID: "session", TaskID: "task"}
	_, err = repo.CreateTurnWithStepStamp(t.Context(), successor)
	require.NoError(t, err)
	err = repo.SettleCompletionIntent(t.Context(), intentID, models.SessionControlEvent{})
	require.Error(t, err)
	stored, err := repo.GetTurn(t.Context(), successor.ID)
	require.NoError(t, err)
	require.Nil(t, stored.CompletedAt)
}

func TestCompletionIntentSettlementRejectsAwayAndBackEntry(t *testing.T) {
	repo := newTurnStepStampTestRepo(t)
	seedTurnStepStampSession(t, repo, "task", "step-a", "session")
	turn := &models.Turn{TaskSessionID: "session", TaskID: "task"}
	_, err := repo.CreateTurnWithStepStamp(t.Context(), turn)
	require.NoError(t, err)
	_, _, entry, err := repo.GetTaskWorkflowStepEntry(t.Context(), "task")
	require.NoError(t, err)
	_, claimed, err := repo.ClaimStepCompletionSignalForTurn(t.Context(), "task", "session", turn.ID, models.PendingStepCompletionSignal{StepID: "step-a", WorkflowStepTransitionID: &entry, Summary: "done", SignaledAt: time.Now().UTC()})
	require.NoError(t, err)
	require.True(t, claimed)
	var intentID string
	require.NoError(t, repo.DB().QueryRow(`SELECT id FROM session_completion_intents`).Scan(&intentID))
	task, err := repo.GetTask(t.Context(), "task")
	require.NoError(t, err)
	for _, step := range []string{"step-b", "step-a"} {
		task.WorkflowStepID = step
		require.NoError(t, repo.UpdateTask(t.Context(), task))
	}
	err = repo.SettleCompletionIntent(t.Context(), intentID, models.SessionControlEvent{})
	require.ErrorIs(t, err, ErrStepCompletionStale)
	stored, err := repo.GetTurn(t.Context(), turn.ID)
	require.NoError(t, err)
	require.Nil(t, stored.CompletedAt)
	var events int
	require.NoError(t, repo.DB().QueryRow(`SELECT COUNT(*) FROM session_control_events`).Scan(&events))
	require.Zero(t, events)
}

func TestCompletionIntentSettlementRollsBackPostWriteFailure(t *testing.T) {
	repo := newTurnStepStampTestRepo(t)
	seedTurnStepStampSession(t, repo, "task", "step-a", "session")
	turn := &models.Turn{TaskSessionID: "session", TaskID: "task"}
	stamped, err := repo.CreateTurnWithStepStamp(t.Context(), turn)
	require.NoError(t, err)
	require.True(t, stamped)
	_, _, entry, err := repo.GetTaskWorkflowStepEntry(t.Context(), "task")
	require.NoError(t, err)
	_, claimed, err := repo.ClaimStepCompletionSignalForTurn(t.Context(), "task", "session", turn.ID, models.PendingStepCompletionSignal{StepID: "step-a", WorkflowStepTransitionID: &entry, Summary: "done", SignaledAt: time.Now().UTC()})
	require.NoError(t, err)
	require.True(t, claimed)
	var intentID string
	require.NoError(t, repo.DB().QueryRow(`SELECT id FROM session_completion_intents`).Scan(&intentID))
	repo.completionSettlementFailpoint = func(stage string) error {
		if stage == "session" {
			return errors.New("injected settlement failure")
		}
		return nil
	}
	err = repo.SettleCompletionIntent(t.Context(), intentID, models.SessionControlEvent{ActorTaskID: "parent", ActorSessionID: "parent-session", Authority: "direct_parent", EvidenceRevision: "test", EvidenceDigest: "digest", Outcome: "settled", Reason: "test"})
	require.EqualError(t, err, "injected settlement failure")
	storedTurn, err := repo.GetTurn(t.Context(), turn.ID)
	require.NoError(t, err)
	require.Nil(t, storedTurn.CompletedAt)
	session, err := repo.GetTaskSession(t.Context(), "session")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateRunning, session.State)
	var state string
	var events int
	require.NoError(t, repo.DB().QueryRow(`SELECT state FROM session_completion_intents WHERE id=?`, intentID).Scan(&state))
	require.NoError(t, repo.DB().QueryRow(`SELECT COUNT(*) FROM session_control_events WHERE completion_intent_id=?`, intentID).Scan(&events))
	require.Equal(t, models.CompletionIntentStatePending, state)
	require.Zero(t, events)
}
