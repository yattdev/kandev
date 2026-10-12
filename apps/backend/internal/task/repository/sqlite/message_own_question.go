package sqlite

import (
	"context"
	"fmt"
	"strings"

	"github.com/kandev/kandev/internal/db/dialect"
	"github.com/kandev/kandev/internal/task/models"
)

// FindOwnClarificationMessages reads only exact task/session clarification
// metadata. It never loads conversation content or a foreign bundle's rows.
func (r *Repository) FindOwnClarificationMessages(ctx context.Context, taskID, sessionID string, questionIDs []string) ([]*models.Message, error) {
	if taskID == "" || sessionID == "" || len(questionIDs) < 1 || len(questionIDs) > 4 {
		return nil, fmt.Errorf("invalid own-question scope")
	}
	args := []any{taskID, sessionID, string(models.MessageTypeClarificationRequest), models.MessageAuthorAgent}
	marks := make([]string, len(questionIDs))
	for i, id := range questionIDs {
		marks[i] = "?"
		args = append(args, id)
	}
	query := fmt.Sprintf(`SELECT id, task_session_id, task_id, turn_id, author_type, author_id,
  '', requests_input, type, metadata, created_at, updated_at FROM task_session_messages
  WHERE task_id = ? AND task_session_id = ? AND type = ? AND author_type = ? AND %s IN (%s)
  ORDER BY created_at, id LIMIT 17`, dialect.JSONExtract(r.ro.DriverName(), "metadata", "question_id"), strings.Join(marks, ","))
	rows, err := r.ro.QueryContext(ctx, r.ro.Rebind(query), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	messages, _, err := scanMessageRows(rows, 0)
	if err != nil {
		return nil, err
	}
	if len(messages) > 16 {
		return nil, fmt.Errorf("own-question identity is ambiguous")
	}
	return messages, nil
}
