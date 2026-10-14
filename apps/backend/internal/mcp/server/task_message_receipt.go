package mcp

import (
	"context"
	"encoding/json"

	ws "github.com/kandev/kandev/pkg/websocket"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const taskMessageReceiptOutputSchema = `{"type":"object","required":["status","retry_allowed","continue_allowed","continuation"],"properties":{"task_id":{"type":"string"},"session_id":{"type":"string"},"intent_id":{"type":"string"},"intent_message_id":{"type":"string"},"message_id":{"type":"string"},"queue_id":{"type":"string"},"turn_id":{"type":"string"},"content_sha256":{"type":"string"},"status":{"type":"string","enum":["not_created","not_found","queued","dispatching","delivery_attempted","recorded_delivery_unknown"]},"retry_allowed":{"type":"boolean"},"continue_allowed":{"type":"boolean"},"continuation":{"type":"string"},"admission_error":{"type":"string"}},"additionalProperties":false}`

func (s *Server) registerTaskMessageReceiptTools() {
	if s.mode == ModeExternal || s.mode == ModeAutomation {
		return
	}
	for _, entry := range []struct {
		name, action, description string
		readOnly                  bool
	}{
		{"get_task_message_status_kandev", ws.ActionMCPGetTaskMessageStatus, "Read only the authenticated sender's own task-message receipt in this workspace. Supply exact task_id/session_id plus intent_id, or an exact legacy message_id. Returns identities and lifecycle metadata only, never prompt bodies or peer messages. An absent intent targeting a terminal session returns session_terminal and retry_allowed=false; exact_session_recovery_required is a disposition, not a launch permission. recorded_delivery_unknown does not prove provider acceptance; never resend an existing identity.", true},
		{"continue_task_message_kandev", ws.ActionMCPContinueTaskMessage, "Recheck readiness of an existing sender-owned queued task message using its exact receipt. Preserves FIFO, clarification, WIP, session-incarnation and writer fences. Does not create a prompt, reapply a move, interrupt a turn, or replay a legacy transcript with uncertain acceptance. Observe existing turn when continuation is denied.", false},
	} {
		tool := mcp.NewTool(entry.name, mcp.WithDescription(entry.description), mcp.WithRawOutputSchema(json.RawMessage(taskMessageReceiptOutputSchema)), mcp.WithReadOnlyHintAnnotation(entry.readOnly), mcp.WithIdempotentHintAnnotation(true), mcp.WithDestructiveHintAnnotation(false), mcp.WithOpenWorldHintAnnotation(false),
			mcp.WithString("task_id", mcp.Description("Exact target task UUID, required with intent_id.")), mcp.WithString("session_id", mcp.Description("Exact target session UUID, required with intent_id.")), mcp.WithString("intent_id", mcp.Description("Exact stable sender intent identifier (8-128 ASCII identifier bytes).")), mcp.WithString("message_id", mcp.Description("Exact existing legacy user-message UUID; mutually exclusive with intent_id.")))
		s.mcpServer.AddTool(tool, s.wrapSensitiveHandler(entry.name, s.taskMessageReceiptHandler(entry.action)))
	}
}

func (s *Server) taskMessageReceiptHandler(action string) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		payload := map[string]interface{}{"sender_task_id": s.taskID, "sender_session_id": s.sessionID}
		for _, key := range []string{"task_id", "session_id", "intent_id", "message_id"} {
			copyOptionalStringArg(payload, req, key)
		}
		var result map[string]interface{}
		if err := s.backend.RequestPayload(ctx, action, payload, &result); err != nil {
			return mcp.NewToolResultError("task message receipt unavailable or ambiguous; correlate before any retry"), nil
		}
		return ownQuestionToolResult(result), nil
	}
}
