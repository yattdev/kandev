package sqlite

import (
	"github.com/kandev/kandev/internal/agentctl/types/streams"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/ownanswer"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestApprovalLinkRejectsOwnershipChangeAfterVerification(t *testing.T) {
	for name, mutation := range map[string]string{
		"executor":            `UPDATE executors_running SET agent_execution_id='replacement' WHERE session_id='answer-session';`,
		"executor-aba":        `UPDATE executors_running SET agent_execution_id='replacement',updated_at='2099-01-01 00:00:00' WHERE session_id='answer-session'; UPDATE executors_running SET agent_execution_id='current-execution' WHERE session_id='answer-session';`,
		"incarnation":         `UPDATE task_sessions SET queue_incarnation_id='replacement' WHERE id='answer-session';`,
		"incarnation-aba":     `UPDATE task_sessions SET queue_incarnation_id='replacement',updated_at='2099-01-01 00:00:00' WHERE id='answer-session'; UPDATE task_sessions SET queue_incarnation_id=(SELECT json_extract(grant_json,'$.session_incarnation_id') FROM own_answer_approval_link_grants WHERE id=NEW.id) WHERE id='answer-session';`,
		"terminal-session":    `UPDATE task_sessions SET state='CANCELLED' WHERE id='answer-session';`,
		"browser-logout":      `DELETE FROM auth_sessions WHERE id=json_extract(NEW.grant_json,'$.creator_session_id');`,
		"browser-role":        `UPDATE users SET role='member' WHERE id=json_extract(NEW.grant_json,'$.creator_user_id');`,
		"browser-role-aba":    `UPDATE users SET role='member',updated_at='2099-01-01 00:00:00' WHERE id=json_extract(NEW.grant_json,'$.creator_user_id'); UPDATE users SET role='admin' WHERE id=json_extract(NEW.grant_json,'$.creator_user_id');`,
		"workspace-owner":     `UPDATE workspaces SET owner_id='foreign' WHERE id='answer-workspace';`,
		"workspace-owner-aba": `UPDATE workspaces SET owner_id='foreign',updated_at='2099-01-01 00:00:00' WHERE id='answer-workspace'; UPDATE workspaces SET owner_id=json_extract(NEW.grant_json,'$.creator_user_id') WHERE id='answer-workspace';`,
	} {
		t.Run(name, func(t *testing.T) {
			r, ctx, b, _, _ := approvalFixture(t)
			g, err := r.createApprovalLinkGrant(ctx, ownanswer.ApprovalLinkRequest{OwnAnswerBinding: b, OptionID: "opt-a"})
			require.NoError(t, err)
			require.NoError(t, r.UpsertExecutorRunning(ctx, &models.ExecutorRunning{ID: b.SessionID, SessionID: b.SessionID, TaskID: b.TaskID, AgentExecutionID: "current-execution", Status: models.ExecutorRunningStatusRunning}))
			ctx = streams.WithMCPExecutionContext(ctx, streams.MCPExecutionContext{TaskID: b.TaskID, SessionID: b.SessionID, ExecutionID: "current-execution"})
			before := snapshotOwnAnswerBoundary(t, r)
			executor, err := r.GetExecutorRunningBySessionID(ctx, b.SessionID)
			require.NoError(t, err)
			// The trigger runs inside the real transaction after all preflight
			// reads, precisely when the grant CAS has succeeded.
			_, err = r.db.Exec(`CREATE TRIGGER change_approval_owner AFTER UPDATE OF state ON own_answer_approval_link_grants WHEN NEW.state='consumed' BEGIN ` + mutation + ` END`)
			require.NoError(t, err)
			_, changed, err := r.consumeApprovalLinkGrant(ctx, b, g.ID, "synthetic-operation", g.Incarnation)
			require.Error(t, err)
			require.False(t, changed)
			require.Equal(t, before, snapshotOwnAnswerBoundary(t, r))
			afterExecutor, err := r.GetExecutorRunningBySessionID(ctx, b.SessionID)
			require.NoError(t, err)
			require.Equal(t, executor, afterExecutor)
			var state string
			require.NoError(t, r.db.Get(&state, `SELECT state FROM own_answer_approval_link_grants WHERE id=?`, g.ID))
			require.Equal(t, "issued", state)
		})
	}
}
