package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/testutil"
)

func TestPostgresCompletionAdmissionWaitsForConcurrentRevisit(t *testing.T) {
	for _, cas := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "cas"}[cas], func(t *testing.T) { checkConcurrentCompletionRevisit(t, cas) })
	}
}

func checkConcurrentCompletionRevisit(t *testing.T, cas bool) {
	t.Helper()
	dsn := testutil.PostgresDSNFromEnv(t)
	db := testutil.OpenIsolatedPostgres(t, dsn)
	repo, err := NewWithDB(db, db, nil)
	require.NoError(t, err)
	admissionCtx := seedCompletionAdmission(t, repo)
	ctx, cancel := context.WithTimeout(admissionCtx, 10*time.Second)
	defer cancel()
	mover := openSecondPostgresConnection(t, dsn, db)
	observer := openSecondPostgresConnection(t, dsn, db)
	var writerPID int
	require.NoError(t, db.GetContext(ctx, &writerPID, `SELECT pg_backend_pid()`))
	task, err := repo.GetTask(ctx, "commit-task")
	require.NoError(t, err)
	tx, err := mover.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	_, _, _, err = repo.readTaskStepInTx(ctx, tx, task.ID)
	require.NoError(t, err)
	task.WorkflowStepID = "other"
	_, _, err = repo.updateTaskTx(ctx, tx, task, "", true, false, nil)
	require.NoError(t, err)
	task.WorkflowStepID = "gate"
	_, _, err = repo.updateTaskTx(ctx, tx, task, "", true, false, nil)
	require.NoError(t, err)
	result := make(chan error, 1)
	done := make(chan struct{})
	go func() { defer close(done); result <- applyCompletionAdmission(repo, ctx, cas) }()
	t.Cleanup(func() { _ = tx.Rollback(); cancel(); <-done })
	require.NoError(t, waitForPostgresLock(ctx, observer, writerPID, done))
	require.NoError(t, tx.Commit())
	select {
	case err := <-result:
		require.ErrorIs(t, err, models.ErrStaleCompletionTurn)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	saved, err := repo.GetTask(t.Context(), task.ID)
	require.NoError(t, err)
	require.Equal(t, "gate", saved.WorkflowStepID)
}
