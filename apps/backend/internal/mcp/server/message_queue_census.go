package mcp

import (
	"context"
	"encoding/json"

	"github.com/kandev/kandev/internal/orchestrator/messagequeue"
	ws "github.com/kandev/kandev/pkg/websocket"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const messageQueueCensusToolName = "get_message_queue_census_kandev"

func (s *Server) registerMessageQueueCensusTool() {
	tool := mcp.NewToolWithRawSchema(messageQueueCensusToolName,
		"Observe bounded pending message identities and content hashes for this running task session. The snapshot may be stale by delivery time; absence does not prove delivery or cancellation.",
		json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`))
	tool.Annotations.ReadOnlyHint = mcp.ToBoolPtr(true)
	tool.Annotations.DestructiveHint = mcp.ToBoolPtr(false)
	s.mcpServer.AddTool(tool, s.wrapHandler(messageQueueCensusToolName, s.messageQueueCensusHandler()))
}

func (s *Server) messageQueueCensusHandler() server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if len(req.GetArguments()) != 0 {
			return mcp.NewToolResultError("queue census requires an empty object"), nil
		}
		var result messagequeue.ActiveQueueCensus
		if err := s.backend.RequestPayload(ctx, ws.ActionMCPGetMessageQueueCensus, map[string]interface{}{}, &result); err != nil {
			return mcp.NewToolResultError("queue census unavailable"), nil
		}
		encoded, err := json.Marshal(result)
		if err != nil || len(encoded) > 65536 {
			return mcp.NewToolResultError("queue census limit exceeded"), nil
		}
		output := mcp.NewToolResultText(string(encoded))
		wrapped, err := json.Marshal(output)
		if err != nil || len(wrapped) > 65536 {
			return mcp.NewToolResultError("queue census limit exceeded"), nil
		}
		return output, nil
	}
}
