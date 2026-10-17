package mcp

import (
	"context"
	"github.com/kandev/kandev/internal/task/ownanswer"
	ws "github.com/kandev/kandev/pkg/websocket"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const ownAnswerToolName = "reconcile_own_question_answer_kandev"

func (s *Server) registerOwnAnswerTool() {
	options := []mcp.ToolOption{
		mcp.WithDescription("Bind an already-delivered native Human user message once to this caller's exact disconnected pending single-choice question. Requires original IDs and exact SHA256 hashes; the backend derives the answer from the stored complete question/answer envelope. No choice/body input, fabricated answer, retry/replay, runtime resume, queue operation or other-session dismissal. Ambiguous/unproven origin or changed evidence fails closed. Unsupported comments/free-text envelopes return ANSWER_FORMAT_UNSUPPORTED, not_reconciled, retry_allowed:false, with a separate Human-confirmed original-question rejection path; never retry unchanged or interpret that disposition as an answered/rejected question. Re-read own status after a timeout."),
		mcp.WithDestructiveHintAnnotation(false), mcp.WithIdempotentHintAnnotation(true), mcp.WithOpenWorldHintAnnotation(false),
	}
	for _, key := range []string{"question_id", "pending_id", "question_message_id", "question_sha256", "answer_message_id", "answer_sha256"} {
		options = append(options, mcp.WithString(key, mcp.Required()))
	}
	for _, key := range []string{"approval_link_grant_id", "approval_link_operation_id"} {
		options = append(options, mcp.WithString(key, mcp.Description("Exact previously issued approval-link identity; both grant and operation IDs required for the reviewed repair.")))
	}
	s.mcpServer.AddTool(mcp.NewTool(ownAnswerToolName, options...), s.wrapSensitiveHandler(ownAnswerToolName, s.ownAnswerHandler()))
}

func (s *Server) ownAnswerHandler() server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		payload := map[string]any{"task_id": s.taskID, "session_id": s.sessionID, "result_mode": ownanswer.DispositionMode}
		for _, key := range []string{"question_id", "pending_id", "question_message_id", "question_sha256", "answer_message_id", "answer_sha256"} {
			value := req.GetString(key, "")
			if value == "" {
				return mcp.NewToolResultError("exact identities and hashes required"), nil
			}
			payload[key] = value
		}
		for _, key := range []string{"approval_link_grant_id", "approval_link_operation_id"} {
			if value := req.GetString(key, ""); value != "" {
				payload[key] = value
			}
		}
		return s.ownQuestionBackendCall(ctx, ws.ActionMCPReconcileOwnQuestionAnswer, payload)
	}
}
