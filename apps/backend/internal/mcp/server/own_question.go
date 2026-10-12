package mcp

import (
	"context"
	"encoding/json"

	ws "github.com/kandev/kandev/pkg/websocket"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const ownQuestionReceiptOutputSchema = `{
 "type":"object",
 "required":["task_id","session_id","status","retry_allowed","questions"],
 "properties":{
  "task_id":{"type":"string"},"session_id":{"type":"string"},
  "pending_id":{"type":"string"},
  "status":{"type":"string","enum":["not_created","pending","answered","rejected","expired","cancelled","unknown"]},
  "retry_allowed":{"type":"boolean"},
  "questions":{"type":"array","maxItems":4,"items":{"type":"object"}}
 },
 "additionalProperties":false
}`

func (s *Server) registerOwnQuestionStatusTool() {
	s.mcpServer.AddTool(mcp.NewTool("get_own_question_status_kandev",
		mcp.WithDescription("Read durable lifecycle metadata and recorded answers for 1-4 exact stable question IDs on this caller's own active task/session. Returns not_created, pending, answered, rejected, expired, cancelled, or unknown. No question prompts, other messages, acknowledgement, retry, or policy change. Correlate the original ID before retry; an existing identity cannot be recreated."),
		mcp.WithRawOutputSchema(json.RawMessage(ownQuestionReceiptOutputSchema)),
		mcp.WithReadOnlyHintAnnotation(true), mcp.WithDestructiveHintAnnotation(false), mcp.WithIdempotentHintAnnotation(true), mcp.WithOpenWorldHintAnnotation(false),
		mcp.WithArray("question_ids", mcp.Required(), mcp.MinItems(1), mcp.MaxItems(4), mcp.Items(map[string]any{"type": "string", "minLength": 1, "maxLength": 128}))),
		s.wrapSensitiveHandler("get_own_question_status_kandev", s.ownQuestionStatusHandler()))
}

func (s *Server) ownQuestionStatusHandler() server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ids := req.GetStringSlice("question_ids", nil)
		if len(ids) < 1 || len(ids) > 4 {
			return mcp.NewToolResultError("question_ids requires 1-4 stable IDs"), nil
		}
		payload := map[string]any{"task_id": s.taskID, "session_id": s.sessionID, "question_ids": ids}
		return s.ownQuestionBackendCall(ctx, ws.ActionMCPGetOwnQuestionStatus, payload)
	}
}

func (s *Server) receiptQuestionHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Check explicit IDs before parseQuestions can generate defaults.
	raw, err := json.Marshal(req.GetArguments()[questionsArg])
	if err != nil {
		return mcp.NewToolResultError("invalid questions"), nil
	}
	var entries []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &entries); err != nil {
		return mcp.NewToolResultError("invalid questions"), nil
	}
	for _, entry := range entries {
		if entry.ID == "" || len(entry.ID) > 128 {
			return mcp.NewToolResultError("receipt mode requires explicit stable question IDs"), nil
		}
	}
	questions, errResult := parseQuestions(req)
	if errResult != nil {
		return errResult, nil
	}
	payload := map[string]any{"task_id": s.taskID, "session_id": s.sessionID, questionsArg: questions, "context": readQuestionContext(req), "return_mode": "receipt"}
	return s.ownQuestionBackendCall(ctx, ws.ActionMCPAskUserQuestion, payload)
}

func (s *Server) ownQuestionBackendCall(ctx context.Context, action string, payload map[string]any) (*mcp.CallToolResult, error) {
	var result map[string]interface{}
	if err := s.backend.RequestPayload(ctx, action, payload, &result); err != nil {
		return mcp.NewToolResultError("own-question operation failed; correlate original status before retry"), nil
	}
	return ownQuestionToolResult(result), nil
}

func ownQuestionToolResult(result map[string]interface{}) *mcp.CallToolResult {
	data, err := json.Marshal(result)
	if err != nil {
		return mcp.NewToolResultError("invalid own-question receipt")
	}
	return mcp.NewToolResultStructured(result, string(data))
}
