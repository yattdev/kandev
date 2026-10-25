package messagequeue

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
)

func TestExactPendingTransitionValidatorFailsClosedForPostgres(t *testing.T) {
	raw, err := sqlx.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	repo := &sqliteRepository{db: sqlx.NewDb(raw.DB, postgresDriverName), exactPendingTransitionsEnabled: true}
	err = repo.ValidateExactPendingTransitionInAuthorityTx(context.Background(), nil, nil, "snapshot", ExactPendingTransition{SessionID: "session", TaskID: "task", WorkspaceID: "ws", SessionIncarnationID: "incarnation", WorkflowID: "wf", WorkflowStepID: "step", QueuedAt: time.Now().UTC(), ResourceVersion: 1, TaskResourceVersion: 1, SessionResourceVersion: 1})
	if !errors.Is(err, ErrExactPendingTransitionUnavailable) {
		t.Fatalf("PostgreSQL validator = %v", err)
	}
}
