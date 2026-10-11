package handlers

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/auth/authn"
	mcpscope "github.com/kandev/kandev/internal/mcp/scope"
	"github.com/kandev/kandev/internal/task/models"
	ws "github.com/kandev/kandev/pkg/websocket"
	"github.com/stretchr/testify/require"
)

type forceRemovalVerifier struct {
	principal mcpscope.Principal
	err       error
}

func (v forceRemovalVerifier) VerifyLiveCaller(context.Context) (mcpscope.Principal, error) {
	return v.principal, v.err
}

func TestCommitForceRemovalUsesVerifiedCallerInsteadOfPayload(t *testing.T) {
	svc, repo := newTestTaskService(t)
	ctx := authn.WithIdentity(context.Background(), authn.Identity{UserID: "admin", Role: authn.RoleAdmin, Synthetic: true})
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-mcp-ws", Name: "Force"}))
	require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: "force-mcp-target", WorkspaceID: "force-mcp-ws", Title: "Target"}))
	task, err := repo.GetTask(ctx, "force-mcp-target")
	require.NoError(t, err)
	grant, err := repo.IssueForceRemovalGrant(ctx, &models.ForceRemovalGrant{TaskID: task.ID, WorkspaceID: task.WorkspaceID, TaskGeneration: task.UpdatedAt, CallerTaskID: "trusted-task", CallerSessionID: "trusted-session", IssuedByUserID: "admin", ExpiresAt: time.Now().Add(time.Minute)})
	require.NoError(t, err)
	h := &Handlers{taskSvc: svc, liveCallerVerifier: forceRemovalVerifier{principal: mcpscope.Principal{CallerTaskID: "trusted-task", CallerSessionID: "trusted-session", WorkspaceID: task.WorkspaceID}}}
	preview, err := svc.PreviewForceRemoval(ctx, task.ID, task.WorkspaceID, task.UpdatedAt.UTC().Format(time.RFC3339Nano))
	require.NoError(t, err)
	payload, _ := json.Marshal(forceRemovalCommitRequest{TaskID: task.ID, WorkspaceID: task.WorkspaceID, ExpectedTaskGeneration: task.UpdatedAt.UTC().Format(time.RFC3339Nano), AdmissionGeneration: "admission", OperationID: "mcp-op", RequestDigest: "request", PreviewDigest: preview.Digest, GrantID: grant.ID})
	resp, err := h.handleCommitForceRemoval(ctx, &ws.Message{ID: "mcp", Action: ws.ActionMCPCommitForceRemoval, Payload: payload})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotEmpty(t, resp.Type)
}
