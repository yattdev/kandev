package sqlite

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/agentctl/types/streams"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/ownanswer"
	"github.com/stretchr/testify/require"
)

func TestApprovalLinkCurrentExecutionBoundary(t *testing.T) {
	for _, state := range []string{"current", "missing", "stale", "stopped"} {
		t.Run(state, func(t *testing.T) {
			r, ctx, b, _, _ := approvalFixture(t)
			g, err := r.createApprovalLinkGrant(ctx, ownanswer.ApprovalLinkRequest{OwnAnswerBinding: b, OptionID: "opt-a"})
			require.NoError(t, err)
			if state != "missing" {
				status := models.ExecutorRunningStatusRunning
				if state == "stopped" {
					status = models.ExecutorRunningStatusStopped
				}
				require.NoError(t, r.UpsertExecutorRunning(ctx, &models.ExecutorRunning{ID: b.SessionID, SessionID: b.SessionID, TaskID: b.TaskID, AgentExecutionID: "current-execution", Status: status}))
			}
			execution := "current-execution"
			if state == "stale" {
				execution = "stale-execution"
			}
			ctx = streams.WithMCPExecutionContext(ctx, streams.MCPExecutionContext{TaskID: b.TaskID, SessionID: b.SessionID, ExecutionID: execution})
			before := snapshotOwnAnswerBoundary(t, r)
			_, changed, err := r.consumeApprovalLinkGrant(ctx, b, g.ID, "synthetic-operation", g.Incarnation)
			if state == "current" {
				require.NoError(t, err)
				require.True(t, changed)
			} else {
				require.Error(t, err)
				require.False(t, changed)
				require.Equal(t, before, snapshotOwnAnswerBoundary(t, r))
			}
		})
	}
}

func TestApprovalLinkPublicBoundaryRemainsClosedToOtherCases(t *testing.T) {
	r, ctx, b, _, _ := approvalFixture(t)
	_, err := r.CreateApprovalLinkGrant(ctx, ownanswer.ApprovalLinkRequest{OwnAnswerBinding: b, OptionID: "opt-a"})
	require.Error(t, err)
	_, _, err = r.ConsumeApprovalLinkGrant(context.Background(), ownanswer.ApprovalLinkCase(), "missing", "synthetic-operation", "incarnation")
	require.Error(t, err)
}
