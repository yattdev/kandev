package mcp

import (
	"context"
	"encoding/json"
	"strings"

	ws "github.com/kandev/kandev/pkg/websocket"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	forceRemovalTaskIDArg                 = "task_id"
	forceRemovalWorkspaceIDArg            = "workspace_id"
	forceRemovalExpectedTaskGenerationArg = "expected_task_generation"
	forceRemovalAdmissionGenerationArg    = "admission_generation"
	forceRemovalOperationIDArg            = "operation_id"
	forceRemovalRequestDigestArg          = "request_digest"
	forceRemovalPreviewDigestArg          = "preview_digest"
	forceRemovalGrantIDArg                = "grant_id"
)

// registerCommitForceRemovalTool registers the admission-only MCP operation.
// The backend derives the live caller from the authenticated session context.
func (s *Server) registerCommitForceRemovalTool() {
	s.mcpServer.AddTool(
		mcp.NewTool("commit_force_removal_kandev",
			mcp.WithDescription("Admit force removal for one exact task using a fresh server-issued preview and grant. This only records the guarded admission; it does not remove workspace resources or hide the task."),
			mcp.WithReadOnlyHintAnnotation(false),
			mcp.WithDestructiveHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
			mcp.WithString(forceRemovalTaskIDArg, mcp.Required(), mcp.Description("Exact target task UUID from the force-removal preview.")),
			mcp.WithString(forceRemovalWorkspaceIDArg, mcp.Required(), mcp.Description("Target workspace UUID from the force-removal preview.")),
			mcp.WithString(forceRemovalExpectedTaskGenerationArg, mcp.Required(), mcp.Description("Target generation returned by the force-removal preview.")),
			mcp.WithString(forceRemovalAdmissionGenerationArg, mcp.Required(), mcp.Description("Fresh admission generation returned by the force-removal preview.")),
			mcp.WithString(forceRemovalOperationIDArg, mcp.Required(), mcp.Description("Idempotency operation ID for this exact admission.")),
			mcp.WithString(forceRemovalRequestDigestArg, mcp.Required(), mcp.Description("Request digest bound to this exact admission.")),
			mcp.WithString(forceRemovalPreviewDigestArg, mcp.Required(), mcp.Description("Fresh server-issued force-removal preview digest.")),
			mcp.WithString(forceRemovalGrantIDArg, mcp.Required(), mcp.Description("Server-issued single-target force-removal grant ID.")),
		),
		s.wrapHandler("commit_force_removal_kandev", s.commitForceRemovalHandler()),
	)
}

func (s *Server) commitForceRemovalHandler() server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if s.taskID == "" || s.sessionID == "" {
			return mcp.NewToolResultError("commit_force_removal_kandev requires a bound task and session"), nil
		}
		payload := forceRemovalPayload(req)
		var result map[string]interface{}
		if err := s.backend.RequestPayload(ctx, ws.ActionMCPCommitForceRemoval, payload, &result); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		data, _ := json.MarshalIndent(result, "", "  ")
		return mcp.NewToolResultText(string(data)), nil
	}
}

func forceRemovalPayload(req mcp.CallToolRequest) map[string]interface{} {
	return map[string]interface{}{
		forceRemovalTaskIDArg:                 strings.TrimSpace(req.GetString(forceRemovalTaskIDArg, "")),
		forceRemovalWorkspaceIDArg:            strings.TrimSpace(req.GetString(forceRemovalWorkspaceIDArg, "")),
		forceRemovalExpectedTaskGenerationArg: strings.TrimSpace(req.GetString(forceRemovalExpectedTaskGenerationArg, "")),
		forceRemovalAdmissionGenerationArg:    strings.TrimSpace(req.GetString(forceRemovalAdmissionGenerationArg, "")),
		forceRemovalOperationIDArg:            strings.TrimSpace(req.GetString(forceRemovalOperationIDArg, "")),
		forceRemovalRequestDigestArg:          strings.TrimSpace(req.GetString(forceRemovalRequestDigestArg, "")),
		forceRemovalPreviewDigestArg:          strings.TrimSpace(req.GetString(forceRemovalPreviewDigestArg, "")),
		forceRemovalGrantIDArg:                strings.TrimSpace(req.GetString(forceRemovalGrantIDArg, "")),
	}
}
