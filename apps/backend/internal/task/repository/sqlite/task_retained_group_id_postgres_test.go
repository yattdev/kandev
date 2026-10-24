package sqlite

import (
	"context"
	"strings"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/testutil"
)

// This dialect-sensitive admission path must also be run by the isolated
// PostgreSQL suite when its approved fixture is available.
func TestPostgresTaskCreationRejectsRetainedWorkspaceGroupID(t *testing.T) {
	db := testutil.OpenIsolatedPostgres(t, testutil.PostgresDSNFromEnv(t))
	repo, err := NewWithDB(db, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := repo.CreateWorkspace(ctx, &models.Workspace{ID: "retained-pg-ws", Name: "Workspace"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateTask(ctx, &models.Task{ID: "retained-pg-task", WorkspaceID: "retained-pg-ws", Title: "Initial"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE task_workspace_group_members (task_id TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO task_workspace_group_members(task_id) VALUES ('retained-pg-task')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM tasks WHERE id = 'retained-pg-task'`); err != nil {
		t.Fatal(err)
	}
	err = repo.CreateTask(ctx, &models.Task{ID: "retained-pg-task", WorkspaceID: "retained-pg-ws", Title: "Reused"})
	if err == nil || !strings.Contains(err.Error(), "retained in workspace group membership") {
		t.Fatalf("retained task ID creation = %v, want denial", err)
	}
	if err := repo.CreateTask(ctx, &models.Task{ID: "fresh-pg-task", WorkspaceID: "retained-pg-ws", Title: "Fresh"}); err != nil {
		t.Fatalf("fresh task ID creation: %v", err)
	}
}
