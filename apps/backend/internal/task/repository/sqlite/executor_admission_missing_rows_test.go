package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
)

func TestUpsertExecutorRunningRequiresExistingMatchingTaskAndSession(t *testing.T) {
	repo := newRepoForSessionTests(t)
	ctx := context.Background()
	seedExecutorRunningCleanupTask(t, repo, "task-present")
	seedExecutorRunningCleanupTask(t, repo, "task-other")
	if err := repo.CreateTaskSession(ctx, &models.TaskSession{ID: "session-present", TaskID: "task-present", State: models.TaskSessionStateRunning}); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, task, session string
		wantErr             bool
	}{
		{"missing task", "task-missing", "session-present", true},
		{"missing session", "task-present", "session-missing", true},
		{"wrong task", "task-other", "session-present", true},
		{"matching rows", "task-present", "session-present", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := repo.UpsertExecutorRunning(ctx, &models.ExecutorRunning{ID: "running-" + tc.name, TaskID: tc.task, SessionID: tc.session})
			if (err != nil) != tc.wantErr {
				t.Fatalf("UpsertExecutorRunning error = %v, want error %v", err, tc.wantErr)
			}
			if tc.name == "missing task" && !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("missing task error = %v, want sql.ErrNoRows", err)
			}
		})
	}
	var count int
	if err := repo.db.GetContext(ctx, &count, `SELECT COUNT(*) FROM executors_running`); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("executor rows = %d, want only the valid row", count)
	}
}
