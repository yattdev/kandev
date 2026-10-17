package sqlite

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/agentctl/types/streams"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/ownanswer"
	"github.com/kandev/kandev/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestApprovalLinkPostgresLifecycleAndReplay(t *testing.T) {
	pool := testutil.OpenIsolatedPostgres(t, testutil.PostgresDSNFromEnv(t))
	r, err := NewWithDB(pool, pool, nil)
	require.NoError(t, err)
	_, ctx, b, _, _ := approvalFixtureOnRepo(t, r)
	g, err := r.createApprovalLinkGrant(ctx, ownanswer.ApprovalLinkRequest{OwnAnswerBinding: b, OptionID: "opt-a"})
	require.NoError(t, err)
	_, changed, err := r.consumeApprovalLinkGrant(ctx, b, g.ID, "synthetic-operation", g.Incarnation)
	require.NoError(t, err)
	require.True(t, changed)
	r, err = NewWithDB(pool, pool, nil)
	require.NoError(t, err)
	_, changed, err = r.consumeApprovalLinkGrant(ctx, b, g.ID, "synthetic-operation", g.Incarnation)
	require.NoError(t, err)
	require.False(t, changed)
	_, _, err = r.consumeApprovalLinkGrant(ctx, b, g.ID, "conflicting-operation", g.Incarnation)
	require.Error(t, err)
}

func TestApprovalLinkPostgresOwnershipLocksSpanCommit(t *testing.T) {
	pool := openIsolatedPostgresMultiConn(t, testutil.PostgresDSNFromEnv(t), 3)
	r, err := NewWithDB(pool, pool, nil)
	require.NoError(t, err)
	_, ctx, b, _, _ := approvalFixtureOnRepo(t, r)
	g, err := r.createApprovalLinkGrant(ctx, ownanswer.ApprovalLinkRequest{OwnAnswerBinding: b, OptionID: "opt-a"})
	require.NoError(t, err)
	require.NoError(t, r.UpsertExecutorRunning(ctx, &models.ExecutorRunning{ID: b.SessionID, SessionID: b.SessionID, TaskID: b.TaskID, AgentExecutionID: "current-execution", Status: models.ExecutorRunningStatusRunning}))
	ctx = streams.WithMCPExecutionContext(ctx, streams.MCPExecutionContext{TaskID: b.TaskID, SessionID: b.SessionID, ExecutionID: "current-execution"})
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	var calls atomic.Int32
	r.clockNow = func() time.Time {
		if calls.Add(1) == 2 {
			close(entered)
			<-release
		}
		return time.Now().UTC()
	}
	results := make(chan error, 1)
	go func() {
		defer close(finished)
		_, _, err := r.consumeApprovalLinkGrant(ctx, b, g.ID, "synthetic-operation", g.Incarnation)
		results <- err
	}()
	t.Cleanup(func() {
		once.Do(func() { close(release) })
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("consumer did not drain")
		}
	})
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("consumer did not reach the post-verification barrier")
	}
	assertApprovalPostgresWritersBlocked(t, r, b, g)
	once.Do(func() { close(release) })
	select {
	case err := <-results:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("consumer did not commit")
	}
	require.NoError(t, r.UpsertExecutorRunning(ctx, &models.ExecutorRunning{ID: b.SessionID, SessionID: b.SessionID, TaskID: b.TaskID, AgentExecutionID: "successor-execution", Status: models.ExecutorRunningStatusRunning}))
	before := snapshotOwnAnswerBoundary(t, r)
	_, changed, err := r.consumeApprovalLinkGrant(ctx, b, g.ID, "synthetic-operation", g.Incarnation)
	require.Error(t, err)
	require.False(t, changed)
	require.Equal(t, before, snapshotOwnAnswerBoundary(t, r))
}

func assertApprovalPostgresWritersBlocked(t *testing.T, r *Repository, b ownanswer.OwnAnswerBinding, g *ownanswer.ApprovalLinkGrant) {
	t.Helper()
	for _, change := range []struct {
		query string
		id    string
	}{
		{`UPDATE executors_running SET agent_execution_id='replacement' WHERE session_id=$1`, b.SessionID},
		{`UPDATE task_sessions SET queue_incarnation_id='replacement' WHERE id=$1`, b.SessionID},
		{`DELETE FROM auth_sessions WHERE id=$1`, g.CreatorSessionID},
		{`UPDATE users SET role='member' WHERE id=$1`, g.CreatorUserID},
		{`UPDATE workspaces SET owner_id='foreign' WHERE id=$1`, b.WorkspaceID},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		tx, err := r.db.BeginTxx(ctx, nil)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback() }()
		_, err = tx.ExecContext(ctx, `SET LOCAL lock_timeout='100ms'`)
		require.NoError(t, err)
		_, err = tx.ExecContext(ctx, change.query, change.id)
		require.ErrorContains(t, err, "lock timeout")
		_ = tx.Rollback()
		cancel()
	}
}
