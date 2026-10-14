package handlers

import (
	"net/url"

	"github.com/kandev/kandev/internal/clarification"
	"github.com/kandev/kandev/internal/task/ownanswer"
)

// This is an instruction for an explicit future Human decision, never an
// automatic rejection, choice, prompt dispatch or runtime resume operation.
func unsupportedOwnAnswerDisposition(b ownanswer.OwnAnswerBinding) map[string]any {
	return map[string]any{
		"status": "not_reconciled", "reason_code": ownanswer.ErrAnswerFormatUnsupported.Error(),
		"question_status": "pending", "agent_disconnected": true,
		"changed": false, "retry_allowed": false,
		"task_id": b.TaskID, "session_id": b.SessionID,
		"question_id": b.QuestionID, "pending_id": b.PendingID,
		"question_message_id": b.QuestionMessageID, "question_sha256": b.QuestionSHA256,
		"answer_message_id": b.AnswerMessageID, "answer_sha256": b.AnswerSHA256,
		"disposition": map[string]any{
			"kind": "human_confirmed_original_rejection", "requires_human_confirmation": true,
			"method": "POST", "path": "/api/v1/clarification/" + url.PathEscape(b.PendingID) + "/respond",
			"body":           map[string]any{"answers": []clarification.Answer{}, "rejected": true},
			"runtime_resume": false, "queue_replay": false,
		},
	}
}
