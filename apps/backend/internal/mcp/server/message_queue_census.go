package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	ws "github.com/kandev/kandev/pkg/websocket"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	messageQueueCensusTool = "get_message_queue_census_kandev"
	removeClaimedQueueTool = "remove_message_queue_entry_kandev"
	queueResultLimit       = 65536
)

func (s *Server) registerMessageQueueTools() {
	s.mcpServer.AddTool(mcp.NewTool(messageQueueCensusTool,
		mcp.WithDescription("List complete body-free pending entries in the current task session with revision claims."),
		mcp.WithReadOnlyHintAnnotation(true),
	), s.wrapSensitiveHandler(messageQueueCensusTool, s.messageQueueCensusHandler()))
	s.mcpServer.AddTool(mcp.NewTool(removeClaimedQueueTool,
		mcp.WithDescription("Remove one unchanged pending entry from the current task session using its census claim."),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithString("entry_id", mcp.Required()),
		mcp.WithString("claim", mcp.Required()),
	), s.wrapSensitiveHandler(removeClaimedQueueTool, s.removeClaimedQueueHandler()))
}

func (s *Server) messageQueueCensusHandler() server.ToolHandlerFunc {
	return func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return s.requestBoundQueueTool(ctx, ws.ActionMCPMessageQueueCensus, map[string]any{})
	}
}

func (s *Server) removeClaimedQueueHandler() server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return s.requestBoundQueueTool(ctx, ws.ActionMCPRemoveClaimedQueueEntry, req.GetArguments())
	}
}

func (s *Server) requestBoundQueueTool(ctx context.Context, action string, arguments map[string]any) (*mcp.CallToolResult, error) {
	if s.taskID == "" || s.sessionID == "" {
		return mcp.NewToolResultError("current task session is required"), nil
	}
	var result json.RawMessage
	if err := s.backend.RequestPayload(ctx, action, arguments, &result); err != nil {
		return mcp.NewToolResultError("queue census request failed"), nil
	}
	if len(result) == 0 || len(result) > queueResultLimit {
		return mcp.NewToolResultError("queue result exceeds response limit"), nil
	}
	toolResult := mcp.NewToolResultText(string(result))
	encoded, err := json.Marshal(toolResult)
	if err != nil || len(encoded) > queueResultLimit {
		return mcp.NewToolResultError("queue result exceeds response limit"), nil
	}
	return toolResult, nil
}

func validateQueueToolRawArguments(toolName string, arguments any) error {
	if toolName != messageQueueCensusTool && toolName != removeClaimedQueueTool {
		return nil
	}
	if arguments == nil {
		if toolName == messageQueueCensusTool {
			return nil
		}
		return fmt.Errorf("queue removal arguments must be an object")
	}
	fields, encoded, err := queueToolObject(arguments)
	if err != nil {
		return err
	}
	if toolName == messageQueueCensusTool {
		if len(fields) != 0 {
			return fmt.Errorf("census arguments must be empty")
		}
		return nil
	}
	return validateClaimedRemovalObject(fields, encoded)
}

func queueToolObject(arguments any) (map[string]any, []byte, error) {
	var encoded []byte
	if raw, ok := arguments.(json.RawMessage); ok {
		if len(raw) > 2048 || hasDuplicateQueueArgumentKeys(raw) {
			return nil, nil, fmt.Errorf("invalid queue arguments")
		}
		encoded = raw
		var decoded any
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return nil, nil, fmt.Errorf("invalid queue arguments")
		}
		arguments = decoded
	}
	fields, ok := arguments.(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf("queue arguments must be an object")
	}
	if encoded == nil {
		var err error
		encoded, err = json.Marshal(arguments)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid queue arguments")
		}
	}
	return fields, encoded, nil
}

func hasDuplicateQueueArgumentKeys(raw json.RawMessage) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return false
	}
	seen := make(map[string]struct{}, 2)
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return false
		}
		name, ok := key.(string)
		if !ok {
			return false
		}
		if _, exists := seen[name]; exists {
			return true
		}
		seen[name] = struct{}{}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return false
		}
	}
	return false
}

func validateClaimedRemovalObject(fields map[string]any, encoded []byte) error {
	if len(encoded) > 2048 {
		return fmt.Errorf("queue removal arguments exceed 2048 bytes")
	}
	if len(fields) != 2 {
		return fmt.Errorf("queue removal requires entry_id and claim")
	}
	entryID, entryOK := fields["entry_id"].(string)
	claim, claimOK := fields["claim"].(string)
	if !entryOK || entryID == "" || len(entryID) > 256 || !claimOK || len(claim) != 64 {
		return fmt.Errorf("invalid queue removal claim")
	}
	for _, ch := range claim {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return fmt.Errorf("invalid queue removal claim")
		}
	}
	return nil
}
