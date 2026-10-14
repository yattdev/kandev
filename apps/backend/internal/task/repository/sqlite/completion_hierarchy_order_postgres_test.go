package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestPostgresCompletionAdmissionLocksSessionBeforeHierarchy(t *testing.T) {
	for _, cas := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "cas"}[cas], func(t *testing.T) {
			dsn := testutil.PostgresDSNFromEnv(t)
			db := testutil.OpenIsolatedPostgres(t, dsn)
			repo, err := NewWithDB(db, db, nil)
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(seedCompletionAdmission(t, repo), 10*time.Second)
			defer cancel()
			blocker := openSecondPostgresConnection(t, dsn, db)
			observer := openSecondPostgresConnection(t, dsn, db)
			tx, err := blocker.BeginTx(ctx, nil)
			require.NoError(t, err)
			defer func() { _ = tx.Rollback() }()
			require.NoError(t, lockSessionTurnWrites(ctx, tx, "pgx", "commit-session"))
			var pid int
			require.NoError(t, db.GetContext(ctx, &pid, `SELECT pg_backend_pid()`))
			done := make(chan struct{})
			result := make(chan error, 1)
			go func() { defer close(done); result <- applyCompletionAdmission(repo, ctx, cas) }()
			t.Cleanup(func() { _ = tx.Rollback(); cancel(); <-done })
			require.NoError(t, waitForPostgresLock(ctx, observer, pid, done))
			// While session admission waits, another hierarchy writer remains eligible.
			probe, err := observer.BeginTx(ctx, nil)
			require.NoError(t, err)
			defer func() { _ = probe.Rollback() }()
			_, err = probe.ExecContext(ctx, `SELECT id FROM workspaces WHERE id = 'ws-turn-stamp' FOR UPDATE NOWAIT`)
			require.NoError(t, err)
			require.NoError(t, probe.Rollback())
			require.NoError(t, tx.Rollback())
			require.NoError(t, <-result)
		})
	}
}
