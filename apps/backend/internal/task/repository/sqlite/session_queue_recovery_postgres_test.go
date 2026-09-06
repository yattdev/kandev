package sqlite

import (
	"context"
	"errors"
	"testing"

	"github.com/kandev/kandev/internal/orchestrator/messagequeue"
	"github.com/kandev/kandev/internal/task/models"
)

// TestPostgresRecoverTaskSessionQueueWaitsForPrimaryPromotion proves the
// authorization read is protected by the same task-row lock as primary
// promotion. It skips unless KANDEV_TEST_POSTGRES_DSN is configured.
func TestPostgresRecoverTaskSessionQueueWaitsForPrimaryPromotion(t *testing.T) {
	repoA, repoB, promotionDB := newTaskPostgresRepoPair(t)
	ctx := context.Background()
	if err := repoA.CreateWorkspace(ctx, &models.Workspace{ID: "workspace-recovery-pg", Name: "Queue recovery"}); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	if err := repoA.CreateTask(ctx, &models.Task{
		ID: "task-recovery-pg", WorkspaceID: "workspace-recovery-pg", Title: "Queue recovery",
	}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	for _, session := range []*models.TaskSession{
		{ID: "destination-pg", TaskID: "task-recovery-pg", IsPrimary: true, State: models.TaskSessionStateWaitingForInput},
		{ID: "source-pg", TaskID: "task-recovery-pg", State: models.TaskSessionStateCompleted},
		{ID: "replacement-pg", TaskID: "task-recovery-pg", State: models.TaskSessionStateWaitingForInput},
	} {
		if err := repoA.CreateTaskSession(ctx, session); err != nil {
			t.Fatalf("CreateTaskSession(%s): %v", session.ID, err)
		}
	}
	queueRepo, err := messagequeue.NewSQLiteRepository(repoA.db, repoA.ro)
	if err != nil {
		t.Fatalf("NewSQLiteRepository: %v", err)
	}
	queued := &messagequeue.QueuedMessage{
		SessionID: "source-pg", TaskID: "task-recovery-pg", Content: "must remain", QueuedBy: messagequeue.QueuedByUser,
	}
	if err := queueRepo.Insert(ctx, queued, 10); err != nil {
		t.Fatalf("insert source queue: %v", err)
	}

	promotionTx, err := promotionDB.BeginTxx(ctx, nil)
	if err != nil {
		t.Fatalf("begin promotion transaction: %v", err)
	}
	defer func() { _ = promotionTx.Rollback() }()
	var lockedTaskID string
	if err := promotionTx.QueryRowContext(ctx, `SELECT id FROM tasks WHERE id = $1 FOR UPDATE`, "task-recovery-pg").Scan(&lockedTaskID); err != nil {
		t.Fatalf("lock task for promotion: %v", err)
	}
	if _, err := promotionTx.ExecContext(ctx, `UPDATE task_sessions SET is_primary = (id = $1) WHERE task_id = $2`,
		"replacement-pg", "task-recovery-pg"); err != nil {
		t.Fatalf("stage primary promotion: %v", err)
	}

	recoveryPID := pgBackendPID(t, repoA.db)
	type recoveryOutcome struct {
		result *messagequeue.QueueRecoveryResult
		err    error
	}
	outcomes := make(chan recoveryOutcome, 1)
	go func() {
		result, recoverErr := repoA.RecoverTaskSessionQueue(ctx, messagequeue.QueueRecoveryScope{
			TaskID: "task-recovery-pg", WorkspaceID: "workspace-recovery-pg",
			SourceSessionID: "source-pg", DestinationSessionID: "destination-pg",
		})
		outcomes <- recoveryOutcome{result: result, err: recoverErr}
	}()
	waitForWaitingLocks(t, repoB.db, recoveryPID, 1, "queue recovery behind primary promotion")
	if err := promotionTx.Commit(); err != nil {
		t.Fatalf("commit promotion transaction: %v", err)
	}
	outcome := <-outcomes
	if outcome.result != nil || !errors.Is(outcome.err, messagequeue.ErrQueueRecoveryUnauthorized) {
		t.Fatalf("recovery outcome = result %#v error %v, want stale-primary denial", outcome.result, outcome.err)
	}
	source, err := queueRepo.ListBySession(ctx, "source-pg")
	if err != nil {
		t.Fatalf("list source queue: %v", err)
	}
	if len(source) != 1 || source[0].ID != queued.ID {
		t.Fatalf("source queue = %#v, want unchanged entry %s", source, queued.ID)
	}
}

// TestPostgresRecoverTaskSessionQueueWaitsForTargetState proves a target that
// resumes before recovery obtains its session lock is rejected atomically.
// It skips unless KANDEV_TEST_POSTGRES_DSN is configured.
func TestPostgresRecoverTaskSessionQueueWaitsForTargetState(t *testing.T) {
	repoA, repoB, stateDB := newTaskPostgresRepoPair(t)
	ctx := context.Background()
	if err := repoA.CreateWorkspace(ctx, &models.Workspace{ID: "workspace-state-pg", Name: "Queue recovery"}); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	if err := repoA.CreateTask(ctx, &models.Task{
		ID: "task-state-pg", WorkspaceID: "workspace-state-pg", Title: "Queue recovery",
	}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	for _, session := range []*models.TaskSession{
		{ID: "destination-state-pg", TaskID: "task-state-pg", IsPrimary: true, State: models.TaskSessionStateWaitingForInput},
		{ID: "source-state-pg", TaskID: "task-state-pg", State: models.TaskSessionStateCompleted},
	} {
		if err := repoA.CreateTaskSession(ctx, session); err != nil {
			t.Fatalf("CreateTaskSession(%s): %v", session.ID, err)
		}
	}
	queueRepo, err := messagequeue.NewSQLiteRepository(repoA.db, repoA.ro)
	if err != nil {
		t.Fatalf("NewSQLiteRepository: %v", err)
	}
	queued := &messagequeue.QueuedMessage{
		SessionID: "source-state-pg", TaskID: "task-state-pg", Content: "must remain", QueuedBy: messagequeue.QueuedByUser,
	}
	if err := queueRepo.Insert(ctx, queued, 10); err != nil {
		t.Fatalf("insert source queue: %v", err)
	}

	stateTx, err := stateDB.BeginTxx(ctx, nil)
	if err != nil {
		t.Fatalf("begin state transaction: %v", err)
	}
	defer func() { _ = stateTx.Rollback() }()
	if _, err := stateTx.ExecContext(ctx, `UPDATE task_sessions SET state = $1 WHERE id = $2`,
		models.TaskSessionStateRunning, "source-state-pg"); err != nil {
		t.Fatalf("stage resumed target: %v", err)
	}

	recoveryPID := pgBackendPID(t, repoA.db)
	type recoveryOutcome struct {
		result *messagequeue.QueueRecoveryResult
		err    error
	}
	outcomes := make(chan recoveryOutcome, 1)
	go func() {
		result, recoverErr := repoA.RecoverTaskSessionQueue(ctx, messagequeue.QueueRecoveryScope{
			TaskID: "task-state-pg", WorkspaceID: "workspace-state-pg",
			SourceSessionID: "source-state-pg", DestinationSessionID: "destination-state-pg",
		})
		outcomes <- recoveryOutcome{result: result, err: recoverErr}
	}()
	waitForWaitingLocks(t, repoB.db, recoveryPID, 1, "queue recovery behind target state update")
	if err := stateTx.Commit(); err != nil {
		t.Fatalf("commit state transaction: %v", err)
	}
	outcome := <-outcomes
	if outcome.result != nil || !errors.Is(outcome.err, messagequeue.ErrQueueRecoveryTargetNotTerminal) {
		t.Fatalf("recovery outcome = result %#v error %v, want resumed-target denial", outcome.result, outcome.err)
	}
	source, err := queueRepo.ListBySession(ctx, "source-state-pg")
	if err != nil {
		t.Fatalf("list source queue: %v", err)
	}
	if len(source) != 1 || source[0].ID != queued.ID {
		t.Fatalf("source queue = %#v, want unchanged entry %s", source, queued.ID)
	}
}
