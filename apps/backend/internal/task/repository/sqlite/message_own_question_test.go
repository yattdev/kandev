package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/testutil"
)

func TestOwnQuestionReaderExactScope(t *testing.T) {
	verifyOwnQuestionScope(t, newRepoForSessionTests(t))
}

func TestOwnQuestionReaderPostgresExactScope(t *testing.T) {
	db := testutil.OpenIsolatedPostgres(t, testutil.PostgresDSNFromEnv(t))
	repo, err := NewWithDB(db, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	verifyOwnQuestionScope(t, repo)
}

func verifyOwnQuestionScope(t *testing.T, repo *Repository) {
	t.Helper()
	ctx := context.Background()
	seedForMsgTest(t, repo, "own-q-task", "own-q-session", "own-q-turn")
	seedForMsgTest(t, repo, "peer-q-task", "peer-q-session", "peer-q-turn")
	now := time.Now().UTC()
	for _, m := range []*models.Message{
		{ID: "own-q", TaskID: "own-q-task", TaskSessionID: "own-q-session", TurnID: "own-q-turn", Type: models.MessageTypeClarificationRequest, AuthorType: "agent", Content: "MUST NOT LOAD BODY", CreatedAt: now, UpdatedAt: now, Metadata: map[string]any{"question_id": "same-q", "pending_id": "own-pending", "status": "answered"}},
		{ID: "human-q", TaskID: "own-q-task", TaskSessionID: "own-q-session", TurnID: "own-q-turn", Type: models.MessageTypeMessage, AuthorType: "user", Content: "MUST NOT LOAD HUMAN", CreatedAt: now, UpdatedAt: now, Metadata: map[string]any{"question_id": "same-q", "pending_id": "human-pending"}},
		{ID: "peer-q", TaskID: "peer-q-task", TaskSessionID: "peer-q-session", TurnID: "peer-q-turn", Type: models.MessageTypeClarificationRequest, AuthorType: "agent", Content: "MUST NOT LOAD PEER", CreatedAt: now, UpdatedAt: now, Metadata: map[string]any{"question_id": "same-q", "pending_id": "peer-pending"}},
	} {
		if err := repo.CreateMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	messages, err := repo.FindOwnClarificationMessages(ctx, "own-q-task", "own-q-session", []string{"same-q"})
	if err != nil || len(messages) != 1 || messages[0].ID != "own-q" || messages[0].Content != "" {
		t.Fatalf("exact-own metadata read failed: %+v, %v", messages, err)
	}
	messages, err = repo.FindOwnClarificationMessages(ctx, "own-q-task", "peer-q-session", []string{"same-q"})
	if err != nil || len(messages) != 0 {
		t.Fatalf("mixed task/session returned rows: %+v, %v", messages, err)
	}
	messages, err = repo.FindOwnClarificationMessages(ctx, "own-q-task", "own-q-session", []string{"unknown-q"})
	if err != nil || len(messages) != 0 {
		t.Fatalf("unknown question returned rows: %+v, %v", messages, err)
	}
}
